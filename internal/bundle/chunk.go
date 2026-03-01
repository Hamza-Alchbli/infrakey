package bundle

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type ChunkPart struct {
	Path   string
	Index  int
	Size   int64
	SHA256 string
}

func SplitBundleIntoChunks(bundlePath string, chunkSizeBytes int64) ([]ChunkPart, error) {
	return SplitBundleIntoChunksWithProgress(bundlePath, chunkSizeBytes, nil)
}

func SplitBundleIntoChunksWithProgress(bundlePath string, chunkSizeBytes int64, onDelta func(int64)) ([]ChunkPart, error) {
	if chunkSizeBytes <= 0 {
		fi, err := os.Stat(bundlePath)
		if err != nil {
			return nil, fmt.Errorf("stat bundle: %w", err)
		}
		return []ChunkPart{{
			Path:  bundlePath,
			Index: 1,
			Size:  fi.Size(),
		}}, nil
	}

	in, err := os.Open(bundlePath)
	if err != nil {
		return nil, fmt.Errorf("open bundle for chunking: %w", err)
	}
	defer in.Close()

	fi, err := in.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat bundle for chunking: %w", err)
	}
	if fi.Size() <= chunkSizeBytes {
		return []ChunkPart{{
			Path:  bundlePath,
			Index: 1,
			Size:  fi.Size(),
		}}, nil
	}

	chunkDir := chunkDirForBundle(bundlePath)
	if err := os.RemoveAll(chunkDir); err != nil {
		return nil, fmt.Errorf("cleanup existing chunk directory %s: %w", chunkDir, err)
	}
	if err := os.MkdirAll(chunkDir, 0o755); err != nil {
		return nil, fmt.Errorf("create chunk directory %s: %w", chunkDir, err)
	}
	success := false
	defer func() {
		if !success {
			_ = os.RemoveAll(chunkDir)
		}
	}()

	parts := make([]ChunkPart, 0, fi.Size()/chunkSizeBytes+1)
	buf := make([]byte, 1024*1024)
	for partIndex := 1; ; partIndex++ {
		partPath := filepath.Join(chunkDir, fmt.Sprintf("part%04d", partIndex))
		out, err := os.OpenFile(partPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, fmt.Errorf("create bundle chunk %d: %w", partIndex, err)
		}

		written := int64(0)
		for written < chunkSizeBytes {
			maxRead := len(buf)
			remaining := chunkSizeBytes - written
			if int64(maxRead) > remaining {
				maxRead = int(remaining)
			}

			n, readErr := in.Read(buf[:maxRead])
			if n > 0 {
				if _, err := out.Write(buf[:n]); err != nil {
					out.Close()
					return nil, fmt.Errorf("write chunk %d: %w", partIndex, err)
				}
				if onDelta != nil {
					onDelta(int64(n))
				}
				written += int64(n)
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				out.Close()
				return nil, fmt.Errorf("read bundle while chunking: %w", readErr)
			}
			if n == 0 {
				break
			}
		}

		if err := out.Close(); err != nil {
			return nil, fmt.Errorf("close chunk %d: %w", partIndex, err)
		}
		if written == 0 {
			if err := os.Remove(partPath); err != nil && !os.IsNotExist(err) {
				return nil, fmt.Errorf("remove empty chunk %d: %w", partIndex, err)
			}
			break
		}

		parts = append(parts, ChunkPart{
			Path:  partPath,
			Index: partIndex,
			Size:  written,
		})
		if written < chunkSizeBytes {
			break
		}
	}

	if len(parts) == 0 {
		return nil, fmt.Errorf("chunking produced no output chunks")
	}
	if err := os.Remove(bundlePath); err != nil {
		return nil, fmt.Errorf("remove original bundle after chunking: %w", err)
	}
	success = true
	return parts, nil
}

func ResolveBundleInputPath(bundlePath, workDir string) (string, error) {
	if st, err := os.Stat(bundlePath); err == nil {
		if st.IsDir() {
			return "", fmt.Errorf("bundle path is a directory: %s", bundlePath)
		}
		return bundlePath, nil
	}

	parts, err := listChunkParts(bundlePath)
	if err != nil {
		return "", err
	}
	joined := filepath.Join(workDir, "bundle.joined")
	if err := joinChunkParts(parts, joined); err != nil {
		return "", err
	}
	return joined, nil
}

func OpenBundleReader(bundlePath string) (io.ReadCloser, int64, error) {
	if st, err := os.Stat(bundlePath); err == nil {
		if st.IsDir() {
			return nil, 0, fmt.Errorf("bundle path is a directory: %s", bundlePath)
		}
		f, err := os.Open(bundlePath)
		if err != nil {
			return nil, 0, fmt.Errorf("open bundle file %s: %w", bundlePath, err)
		}
		return f, st.Size(), nil
	}

	parts, err := listChunkParts(bundlePath)
	if err != nil {
		return nil, 0, err
	}
	files := make([]*os.File, 0, len(parts))
	readers := make([]io.Reader, 0, len(parts))
	closers := make([]io.Closer, 0, len(parts))
	total := int64(0)
	for _, p := range parts {
		f, err := os.Open(p.Path)
		if err != nil {
			for _, opened := range files {
				_ = opened.Close()
			}
			return nil, 0, fmt.Errorf("open bundle chunk %s: %w", p.Path, err)
		}
		files = append(files, f)
		readers = append(readers, f)
		closers = append(closers, f)
		total += p.Size
	}
	return &multiReadCloser{
		reader:  io.MultiReader(readers...),
		closers: closers,
	}, total, nil
}

func listChunkParts(bundlePath string) ([]ChunkPart, error) {
	chunkDir := chunkDirForBundle(bundlePath)
	st, err := os.Stat(chunkDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("bundle file not found: %s (expected chunks in %s)", bundlePath, chunkDir)
		}
		return nil, fmt.Errorf("stat chunk directory %s: %w", chunkDir, err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("chunk path is not a directory: %s", chunkDir)
	}
	dirEntries, err := os.ReadDir(chunkDir)
	if err != nil {
		return nil, fmt.Errorf("read chunk directory %s: %w", chunkDir, err)
	}
	paths := make([]string, 0, len(dirEntries))
	for _, de := range dirEntries {
		if de.IsDir() {
			continue
		}
		paths = append(paths, filepath.Join(chunkDir, de.Name()))
	}
	parts, err := readChunkParts(paths...)
	if err != nil {
		return nil, err
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("no valid bundle chunks found in %s", chunkDir)
	}
	return parts, nil
}

func readChunkParts(paths ...string) ([]ChunkPart, error) {
	parts := make([]ChunkPart, 0, len(paths))
	for _, p := range paths {
		base := filepath.Base(p)
		idx, ok := parseChunkIndex(base)
		if !ok {
			continue
		}
		fi, err := os.Stat(p)
		if err != nil {
			return nil, fmt.Errorf("stat bundle chunk %s: %w", p, err)
		}
		parts = append(parts, ChunkPart{
			Path:  p,
			Index: idx,
			Size:  fi.Size(),
		})
	}
	sort.Slice(parts, func(i, j int) bool { return parts[i].Index < parts[j].Index })
	for i, p := range parts {
		expected := i + 1
		if p.Index != expected {
			return nil, fmt.Errorf("missing bundle chunk: expected part%04d", expected)
		}
	}
	return parts, nil
}

func parseChunkIndex(base string) (int, bool) {
	if !strings.HasPrefix(base, "part") {
		return 0, false
	}
	num := strings.TrimPrefix(base, "part")
	if num == "" {
		return 0, false
	}
	idx, err := strconv.Atoi(num)
	if err != nil || idx <= 0 {
		return 0, false
	}
	return idx, true
}

func chunkDirForBundle(bundlePath string) string {
	return bundlePath + ".parts"
}

func joinChunkParts(parts []ChunkPart, outPath string) error {
	out, err := os.OpenFile(outPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create joined bundle: %w", err)
	}
	defer out.Close()

	for _, p := range parts {
		in, err := os.Open(p.Path)
		if err != nil {
			return fmt.Errorf("open bundle chunk %s: %w", p.Path, err)
		}
		buf := make([]byte, 1024*1024)
		if _, err := io.CopyBuffer(out, in, buf); err != nil {
			in.Close()
			return fmt.Errorf("append bundle chunk %s: %w", p.Path, err)
		}
		if err := in.Close(); err != nil {
			return fmt.Errorf("close bundle chunk %s: %w", p.Path, err)
		}
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close joined bundle: %w", err)
	}
	return nil
}

type multiReadCloser struct {
	reader  io.Reader
	closers []io.Closer
}

func (m *multiReadCloser) Read(p []byte) (int, error) {
	return m.reader.Read(p)
}

func (m *multiReadCloser) Close() error {
	var firstErr error
	for _, c := range m.closers {
		if err := c.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
