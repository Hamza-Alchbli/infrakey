package bundle

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var fixedMtime = time.Unix(0, 0)

func CreateDeterministicTar(srcDir, outTarPath string) error {
	f, err := os.Create(outTarPath)
	if err != nil {
		return fmt.Errorf("create tar: %w", err)
	}
	defer f.Close()

	return CreateDeterministicTarToWriter(srcDir, f)
}

func CreateDeterministicTarToWriter(srcDir string, w io.Writer) error {
	tw := tar.NewWriter(w)
	defer tw.Close()
	copyBuf := make([]byte, 1024*1024)

	var paths []string
	err := filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == srcDir {
			return nil
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk source for tar: %w", err)
	}
	sort.Strings(paths)

	for _, relSlash := range paths {
		relNative := filepath.FromSlash(relSlash)
		absPath := filepath.Join(srcDir, relNative)
		info, err := os.Lstat(absPath)
		if err != nil {
			return fmt.Errorf("stat %s: %w", absPath, err)
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return fmt.Errorf("tar header %s: %w", absPath, err)
		}
		header.Name = relSlash
		header.ModTime = fixedMtime
		header.AccessTime = fixedMtime
		header.ChangeTime = fixedMtime

		switch {
		case info.Mode().IsRegular():
			header.Typeflag = tar.TypeReg
		case info.IsDir():
			header.Typeflag = tar.TypeDir
			if !strings.HasSuffix(header.Name, "/") {
				header.Name += "/"
			}
		default:
			return fmt.Errorf("unsupported file type in payload: %s", absPath)
		}

		if err := tw.WriteHeader(header); err != nil {
			return fmt.Errorf("write tar header %s: %w", absPath, err)
		}
		if info.Mode().IsRegular() {
			in, err := os.Open(absPath)
			if err != nil {
				return fmt.Errorf("open %s: %w", absPath, err)
			}
			if _, err := io.CopyBuffer(tw, in, copyBuf); err != nil {
				in.Close()
				return fmt.Errorf("write tar body %s: %w", absPath, err)
			}
			if err := in.Close(); err != nil {
				return fmt.Errorf("close %s: %w", absPath, err)
			}
		}
	}
	return nil
}

func HashDeterministicTar(srcDir string) (string, error) {
	h := sha256.New()
	if err := CreateDeterministicTarToWriter(srcDir, h); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func ExtractTar(tarPath, dest string) error {
	return ExtractTarWithProgress(tarPath, dest, nil)
}

func ExtractTarWithProgress(tarPath, dest string, onDelta func(int64)) error {
	f, err := os.Open(tarPath)
	if err != nil {
		return fmt.Errorf("open tar: %w", err)
	}
	defer f.Close()
	return ExtractTarReaderWithProgress(f, dest, onDelta)
}

func ExtractTarReaderWithProgress(r io.Reader, dest string, onDelta func(int64)) error {
	var reader io.Reader = r
	if onDelta != nil {
		reader = &countingReader{r: r, onDelta: onDelta}
	}
	tr := tar.NewReader(reader)
	copyBuf := make([]byte, 1024*1024)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar: %w", err)
		}

		cleanName := filepath.Clean(filepath.FromSlash(hdr.Name))
		if cleanName == "." || cleanName == string(filepath.Separator) {
			continue
		}
		if strings.HasPrefix(cleanName, ".."+string(filepath.Separator)) || cleanName == ".." {
			return fmt.Errorf("tar entry escapes destination: %q", hdr.Name)
		}
		target := filepath.Join(dest, cleanName)

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode)); err != nil {
				return fmt.Errorf("mkdir %s: %w", target, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("mkdir parent %s: %w", target, err)
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode))
			if err != nil {
				return fmt.Errorf("create %s: %w", target, err)
			}
			if _, err := io.CopyBuffer(out, tr, copyBuf); err != nil {
				out.Close()
				return fmt.Errorf("write %s: %w", target, err)
			}
			if err := out.Close(); err != nil {
				return fmt.Errorf("close %s: %w", target, err)
			}
		default:
			return fmt.Errorf("unsupported tar entry type %d for %s", hdr.Typeflag, hdr.Name)
		}
	}

	return nil
}

type countingReader struct {
	r       io.Reader
	onDelta func(int64)
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 && c.onDelta != nil {
		c.onDelta(int64(n))
	}
	return n, err
}
