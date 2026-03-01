package bundle

import (
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"infrakey/internal/compose"
	"infrakey/internal/crypto"
	"infrakey/internal/discovery"
	"infrakey/internal/manifest"
	"infrakey/internal/pathmap"
)

type SnapshotOptions struct {
	RootDir        string
	OutBundle      string
	Recipient      string
	IdentityOut    string
	ComposePaths   []string
	FullCopy       bool
	ChunkSizeBytes int64
	Progress       ProgressFunc
}

type ProgressEvent struct {
	Stage      string
	BytesDone  int64
	BytesTotal int64
}

type ProgressFunc func(ProgressEvent)

type SnapshotSummary struct {
	ComposeFiles          int
	CapturedFiles         int
	ExternalFiles         int
	SkippedMissing        int
	Chunked               bool
	ChunkCount            int
	ChunkPaths            []string
	Manifest              manifest.Manifest
	GeneratedIdentityPath string
}

type SnapshotPlan struct {
	RootDir               string
	OutBundle             string
	ComposePaths          []string
	Entries               []manifest.Entry
	ExternalEntries       []manifest.Entry
	SkippedMissing        int
	WouldGenerateIdentity bool
	IdentityPath          string
	Manifest              manifest.Manifest
}

type snapshotBuildResult struct {
	summary               SnapshotSummary
	composeFiles          []string
	entries               []manifest.Entry
	recipient             string
	validationKeyPath     string
	wouldGenerateIdentity bool
}

type sourceMeta struct {
	kind  string
	mode  os.FileMode
	isDir bool
}

func buildSnapshotPlan(opts SnapshotOptions, dryRun bool) (snapshotBuildResult, error) {
	var result snapshotBuildResult

	if runtime.GOOS != "linux" {
		return result, fmt.Errorf("linux-only MVP: current platform is %s", runtime.GOOS)
	}
	if err := crypto.EnsureAgeInstalled(); err != nil {
		return result, err
	}

	rootAbs, err := filepath.Abs(opts.RootDir)
	if err != nil {
		return result, fmt.Errorf("resolve root path: %w", err)
	}
	st, err := os.Stat(rootAbs)
	if err != nil {
		return result, fmt.Errorf("stat root path: %w", err)
	}
	if !st.IsDir() {
		return result, fmt.Errorf("root path must be a directory")
	}

	composeFiles, err := resolveComposeFiles(rootAbs, opts.ComposePaths)
	if err != nil {
		return result, err
	}
	if len(composeFiles) == 0 {
		return result, fmt.Errorf("no compose files found under %s", rootAbs)
	}
	result.composeFiles = append([]string(nil), composeFiles...)

	recipient := strings.TrimSpace(opts.Recipient)
	summary := SnapshotSummary{ComposeFiles: len(composeFiles)}
	validationKeyPath := ""
	if recipient == "" {
		if opts.IdentityOut == "" {
			opts.IdentityOut = "identity.key"
		}
		identityAbs, err := filepath.Abs(opts.IdentityOut)
		if err != nil {
			return result, fmt.Errorf("resolve identity-out path: %w", err)
		}
		if _, err := os.Stat(identityAbs); err == nil {
			return result, fmt.Errorf("identity key already exists: %s", identityAbs)
		} else if !os.IsNotExist(err) {
			return result, fmt.Errorf("stat identity key path: %w", err)
		}
		if dryRun {
			summary.GeneratedIdentityPath = identityAbs
			result.wouldGenerateIdentity = true
			recipient = "dry-run-recipient"
		} else {
			recipient, err = crypto.GenerateIdentity(identityAbs)
			if err != nil {
				return result, err
			}
			summary.GeneratedIdentityPath = identityAbs
			validationKeyPath = identityAbs
		}
	}

	sources := map[string]sourceMeta{}
	for _, p := range composeFiles {
		fi, err := os.Stat(p)
		if err != nil {
			return result, fmt.Errorf("stat compose file %s: %w", p, err)
		}
		if !fi.Mode().IsRegular() {
			return result, fmt.Errorf("compose path is not a regular file: %s", p)
		}
		if err := registerCapturedSource(sources, p, manifest.KindCompose, fi); err != nil {
			return result, err
		}

		if opts.FullCopy {
			projectDir := filepath.Dir(p)
			dirInfo, err := os.Stat(projectDir)
			if err != nil {
				return result, fmt.Errorf("stat compose project dir %s: %w", projectDir, err)
			}
			if !dirInfo.IsDir() {
				return result, fmt.Errorf("compose project path is not a directory: %s", projectDir)
			}
			if err := registerCapturedSource(sources, projectDir, manifest.KindVolume, dirInfo); err != nil {
				return result, err
			}
		}
	}

	composeRewritesByPath := map[string][]manifest.PathReplacement{}
	skippedMissing := 0
	for _, composePath := range composeFiles {
		parsed, err := compose.ParseFile(composePath)
		if err != nil {
			return result, fmt.Errorf("parse compose file %s: %w", composePath, err)
		}
		for _, m := range parsed.Mentions {
			if includeAsCapturedFile(m.Kind, opts.FullCopy) {
				fi, err := os.Stat(m.ResolvedAbs)
				if err != nil {
					skippedMissing++
					continue
				}
				isDir := fi.IsDir()
				isRegular := fi.Mode().IsRegular()
				if isDir {
					if !(opts.FullCopy && m.Kind == compose.MentionVolume) {
						continue
					}
				} else if !isRegular {
					continue
				}
				entryKind := kindFromMention(m.Kind)
				if !pathmap.IsInsideRoot(rootAbs, m.ResolvedAbs) {
					entryKind = manifest.KindExternal
				}
				if err := registerCapturedSource(sources, m.ResolvedAbs, entryKind, fi); err != nil {
					return result, err
				}
			}

			if !m.OriginalAbsolute {
				continue
			}
			restoreRel, _ := pathmap.ComputeRestoreRelPath(rootAbs, m.ResolvedAbs)
			if restoreRel == "" {
				continue
			}
			composeRewritesByPath[composePath] = addReplacementUnique(composeRewritesByPath[composePath], manifest.PathReplacement{
				OriginalPath: m.Original,
				RestoredPath: restoreRel,
			})
		}
	}

	filePaths := make([]string, 0, len(sources))
	for p := range sources {
		filePaths = append(filePaths, p)
	}
	sort.Strings(filePaths)

	entries := make([]manifest.Entry, 0, len(filePaths))
	outsideIDs := make([]string, 0)
	entryIDByPath := map[string]string{}
	for _, p := range filePaths {
		id := stableEntryID(p)
		if _, ok := entryIDByPath[p]; ok {
			return result, fmt.Errorf("duplicate entry source path %s", p)
		}
		entryIDByPath[p] = id
		meta := sources[p]

		sha := ""
		if meta.isDir {
			if dryRun {
				sha, err = HashDeterministicTar(p)
				if err != nil {
					return result, fmt.Errorf("hash directory %s: %w", p, err)
				}
			} else {
				sha = "pending"
			}
		} else {
			sha, err = fileSHA256(p)
			if err != nil {
				return result, fmt.Errorf("hash %s: %w", p, err)
			}
		}
		restoreRel, inRoot := pathmap.ComputeRestoreRelPath(rootAbs, p)
		sourceRel := ""
		if inRoot {
			rel, err := filepath.Rel(rootAbs, p)
			if err == nil {
				sourceRel = filepath.ToSlash(rel)
			}
		} else {
			outsideIDs = append(outsideIDs, id)
		}
		entry := manifest.Entry{
			ID:             id,
			Kind:           meta.kind,
			SourceAbsPath:  p,
			SourceRelPath:  sourceRel,
			RestoreRelPath: restoreRel,
			SHA256:         sha,
			Mode:           fmt.Sprintf("%04o", meta.mode.Perm()),
		}
		if meta.isDir {
			entry.EntryType = manifest.EntryTypeDirectory
			entry.DataFormat = manifest.DataFormatTarDir
		}
		entries = append(entries, entry)
	}

	composeRewrites := make([]manifest.ComposeRewrite, 0, len(composeRewritesByPath))
	for composePath, reps := range composeRewritesByPath {
		entryID, ok := entryIDByPath[composePath]
		if !ok {
			continue
		}
		composeRewrites = append(composeRewrites, manifest.ComposeRewrite{
			ComposeEntryID: entryID,
			Replacements:   reps,
		})
	}
	sort.Slice(composeRewrites, func(i, j int) bool { return composeRewrites[i].ComposeEntryID < composeRewrites[j].ComposeEntryID })

	snapshotID, err := randomHex(12)
	if err != nil {
		return result, err
	}
	mf := manifest.Manifest{
		PCIVersion:         manifest.CurrentPCIVersion,
		SnapshotID:         snapshotID,
		CreatedAt:          time.Now().UTC().Format(time.RFC3339),
		SourceRoot:         rootAbs,
		Entries:            entries,
		ComposeRewrites:    composeRewrites,
		OutsideRootEntries: outsideIDs,
	}
	if err := mf.Validate(); err != nil {
		return result, fmt.Errorf("build manifest: %w", err)
	}

	summary.CapturedFiles = len(entries)
	summary.ExternalFiles = len(outsideIDs)
	summary.SkippedMissing = skippedMissing
	summary.Manifest = mf

	result.summary = summary
	result.entries = entries
	result.recipient = recipient
	result.validationKeyPath = validationKeyPath
	return result, nil
}

func PlanSnapshot(opts SnapshotOptions) (SnapshotPlan, error) {
	result, err := buildSnapshotPlan(opts, true)
	if err != nil {
		return SnapshotPlan{}, err
	}
	outAbs, err := filepath.Abs(opts.OutBundle)
	if err != nil {
		return SnapshotPlan{}, fmt.Errorf("resolve output bundle path: %w", err)
	}

	externalSet := result.summary.Manifest.OutsideRootSet()
	externalEntries := make([]manifest.Entry, 0, len(externalSet))
	for _, e := range result.entries {
		if _, ok := externalSet[e.ID]; ok {
			externalEntries = append(externalEntries, e)
		}
	}

	return SnapshotPlan{
		RootDir:               result.summary.Manifest.SourceRoot,
		OutBundle:             outAbs,
		ComposePaths:          append([]string(nil), result.composeFiles...),
		Entries:               append([]manifest.Entry(nil), result.entries...),
		ExternalEntries:       externalEntries,
		SkippedMissing:        result.summary.SkippedMissing,
		WouldGenerateIdentity: result.wouldGenerateIdentity,
		IdentityPath:          result.summary.GeneratedIdentityPath,
		Manifest:              result.summary.Manifest,
	}, nil
}

func CreateSnapshot(opts SnapshotOptions) (SnapshotSummary, error) {
	result, err := buildSnapshotPlan(opts, false)
	if err != nil {
		return SnapshotSummary{}, err
	}

	outAbs, err := filepath.Abs(opts.OutBundle)
	if err != nil {
		return SnapshotSummary{}, fmt.Errorf("resolve output bundle path: %w", err)
	}
	outDir := filepath.Dir(outAbs)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return SnapshotSummary{}, fmt.Errorf("create output directory: %w", err)
	}

	stage, err := os.MkdirTemp(outDir, ".infrakey-snapshot-")
	if err != nil {
		return SnapshotSummary{}, fmt.Errorf("create snapshot temp dir in output directory: %w", err)
	}
	defer os.RemoveAll(stage)

	payloadDir := filepath.Join(stage, "payload")
	if err := os.MkdirAll(filepath.Join(payloadDir, "files"), 0o700); err != nil {
		return SnapshotSummary{}, fmt.Errorf("create payload dir: %w", err)
	}

	manifestIndexByID := make(map[string]int, len(result.summary.Manifest.Entries))
	for i, e := range result.summary.Manifest.Entries {
		manifestIndexByID[e.ID] = i
	}

	stageTotal := int64(0)
	stageDone := int64(0)
	for _, e := range result.entries {
		n, err := estimateStageBytes(e)
		if err == nil {
			stageTotal += n
		}
	}
	reportSnapshotProgress(opts.Progress, "staging", stageDone, stageTotal)

	for _, e := range result.entries {
		src := e.SourceAbsPath
		dst := filepath.Join(payloadDir, "files", e.ID)
		switch e.EffectiveDataFormat() {
		case manifest.DataFormatRaw:
			_, err := copyFileWithProgress(src, dst, func(n int64) {
				stageDone += n
				reportSnapshotProgress(opts.Progress, "staging", stageDone, stageTotal)
			})
			if err != nil {
				return SnapshotSummary{}, fmt.Errorf("stage entry %s: %w", e.ID, err)
			}
		case manifest.DataFormatTarDir:
			written, sha, err := createDeterministicTarFileWithProgress(src, dst, func(n int64) {
				stageDone += n
				reportSnapshotProgress(opts.Progress, "staging", stageDone, stageTotal)
			})
			if err != nil {
				return SnapshotSummary{}, fmt.Errorf("stage directory entry %s: %w", e.ID, err)
			}
			_ = written
			for i := range result.entries {
				if result.entries[i].ID == e.ID {
					result.entries[i].SHA256 = sha
					break
				}
			}
			if idx, ok := manifestIndexByID[e.ID]; ok {
				result.summary.Manifest.Entries[idx].SHA256 = sha
			}
		default:
			return SnapshotSummary{}, fmt.Errorf("unsupported data format for entry %s: %s", e.ID, e.DataFormat)
		}
	}
	if err := result.summary.Manifest.Validate(); err != nil {
		return SnapshotSummary{}, fmt.Errorf("manifest validation after staging: %w", err)
	}
	if err := manifest.WriteToFile(filepath.Join(payloadDir, "manifest.pci.json"), result.summary.Manifest); err != nil {
		return SnapshotSummary{}, err
	}

	tarPath := filepath.Join(stage, "payload.tar")
	reportSnapshotProgress(opts.Progress, "packaging", 0, 0)
	packDone := int64(0)
	if _, _, err := createDeterministicTarFileWithProgress(payloadDir, tarPath, func(n int64) {
		packDone += n
		reportSnapshotProgress(opts.Progress, "packaging", packDone, 0)
	}); err != nil {
		return SnapshotSummary{}, err
	}

	reportSnapshotProgress(opts.Progress, "encrypting", 0, 0)
	if err := crypto.EncryptFile(tarPath, outAbs, result.recipient); err != nil {
		return SnapshotSummary{}, err
	}

	if result.validationKeyPath != "" {
		reportSnapshotProgress(opts.Progress, "validating", 0, 0)
		if err := crypto.DecryptToDiscard(outAbs, result.validationKeyPath); err != nil {
			return SnapshotSummary{}, fmt.Errorf("post-encryption validation failed: %w", err)
		}
	}

	summary := result.summary
	summary.Chunked = false
	summary.ChunkCount = 1
	summary.ChunkPaths = []string{outAbs}
	if opts.ChunkSizeBytes > 0 {
		reportSnapshotProgress(opts.Progress, "chunking", 0, 0)
		chunkDone := int64(0)
		chunks, err := SplitBundleIntoChunksWithProgress(outAbs, opts.ChunkSizeBytes, func(n int64) {
			chunkDone += n
			reportSnapshotProgress(opts.Progress, "chunking", chunkDone, 0)
		})
		if err != nil {
			return SnapshotSummary{}, fmt.Errorf("chunk encrypted bundle: %w", err)
		}
		summary.ChunkCount = len(chunks)
		summary.ChunkPaths = summary.ChunkPaths[:0]
		for _, c := range chunks {
			summary.ChunkPaths = append(summary.ChunkPaths, c.Path)
		}
		if len(chunks) > 1 {
			summary.Chunked = true
		}
	}
	if err := writeInspectSidecar(outAbs, summary.Chunked, summary.Manifest, result.recipient); err != nil {
		return SnapshotSummary{}, err
	}

	return summary, nil
}

func includeAsCapturedFile(mentionKind string, fullCopy bool) bool {
	switch mentionKind {
	case compose.MentionEnvFile, compose.MentionSecret, compose.MentionConfig, compose.MentionCert:
		return true
	case compose.MentionVolume:
		return fullCopy
	default:
		return false
	}
}

func estimateStageBytes(e manifest.Entry) (int64, error) {
	switch e.EffectiveDataFormat() {
	case manifest.DataFormatRaw:
		fi, err := os.Stat(e.SourceAbsPath)
		if err != nil {
			return 0, err
		}
		if !fi.Mode().IsRegular() {
			return 0, nil
		}
		return fi.Size(), nil
	case manifest.DataFormatTarDir:
		return estimateDirRegularBytes(e.SourceAbsPath)
	default:
		return 0, nil
	}
}

func estimateDirRegularBytes(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return total, nil
}

func kindFromMention(mentionKind string) string {
	switch mentionKind {
	case compose.MentionEnvFile:
		return manifest.KindEnv
	case compose.MentionSecret:
		return manifest.KindSecret
	case compose.MentionConfig:
		return manifest.KindConfig
	case compose.MentionCert:
		return manifest.KindCert
	case compose.MentionVolume:
		return manifest.KindVolume
	default:
		return manifest.KindExternal
	}
}

func mergeEntryKind(existing, next string) string {
	if existing == "" {
		return next
	}
	priority := map[string]int{
		manifest.KindCompose:  6,
		manifest.KindCert:     5,
		manifest.KindSecret:   4,
		manifest.KindConfig:   3,
		manifest.KindVolume:   2,
		manifest.KindEnv:      2,
		manifest.KindExternal: 1,
	}
	if priority[next] > priority[existing] {
		return next
	}
	return existing
}

func registerCapturedSource(sources map[string]sourceMeta, path, kind string, fi os.FileInfo) error {
	isDir := fi.IsDir()
	existing, ok := sources[path]
	if !ok {
		sources[path] = sourceMeta{
			kind:  kind,
			mode:  fi.Mode(),
			isDir: isDir,
		}
		return nil
	}
	if existing.isDir != isDir {
		return fmt.Errorf("path is referenced as both file and directory: %s", path)
	}
	existing.kind = mergeEntryKind(existing.kind, kind)
	existing.mode = fi.Mode()
	sources[path] = existing
	return nil
}

func resolveComposeFiles(rootAbs string, selected []string) ([]string, error) {
	if len(selected) == 0 {
		return discovery.DiscoverComposeFiles(rootAbs)
	}

	seen := map[string]struct{}{}
	out := make([]string, 0, len(selected))
	for _, p := range selected {
		if strings.TrimSpace(p) == "" {
			continue
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			return nil, fmt.Errorf("resolve selected compose path %q: %w", p, err)
		}
		if !pathmap.IsInsideRoot(rootAbs, abs) {
			return nil, fmt.Errorf("selected compose path outside root: %s", abs)
		}
		fi, err := os.Stat(abs)
		if err != nil {
			return nil, fmt.Errorf("stat selected compose path %q: %w", abs, err)
		}
		if !fi.Mode().IsRegular() {
			return nil, fmt.Errorf("selected compose path is not a regular file: %s", abs)
		}
		if _, ok := seen[abs]; ok {
			continue
		}
		seen[abs] = struct{}{}
		out = append(out, abs)
	}

	sort.Strings(out)
	if len(out) == 0 {
		return nil, fmt.Errorf("no compose files selected")
	}
	return out, nil
}

func addReplacementUnique(in []manifest.PathReplacement, repl manifest.PathReplacement) []manifest.PathReplacement {
	for _, existing := range in {
		if existing.OriginalPath == repl.OriginalPath && existing.RestoredPath == repl.RestoredPath {
			return in
		}
	}
	return append(in, repl)
}

func stableEntryID(sourcePath string) string {
	h := sha1.Sum([]byte(filepath.Clean(sourcePath)))
	return hex.EncodeToString(h[:16])
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func copyFile(src, dst string) error {
	_, err := copyFileWithProgress(src, dst, nil)
	return err
}

func copyFileWithProgress(src, dst string, onDelta func(int64)) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	buf := make([]byte, 1024*1024)
	written, err := io.CopyBuffer(out, in, buf)
	if err != nil {
		out.Close()
		return 0, err
	}
	if onDelta != nil && written > 0 {
		onDelta(written)
	}
	if err := out.Close(); err != nil {
		return written, err
	}
	return written, nil
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read randomness: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func createDeterministicTarFileWithProgress(srcDir, outTarPath string, onDelta func(int64)) (int64, string, error) {
	out, err := os.OpenFile(outTarPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, "", fmt.Errorf("create tar output %s: %w", outTarPath, err)
	}
	cw := &countingWriter{w: out, onDelta: onDelta}
	hasher := sha256.New()
	mw := io.MultiWriter(cw, hasher)
	if err := CreateDeterministicTarToWriter(srcDir, mw); err != nil {
		out.Close()
		return cw.written, "", err
	}
	if err := out.Close(); err != nil {
		return cw.written, "", err
	}
	return cw.written, hex.EncodeToString(hasher.Sum(nil)), nil
}

func reportSnapshotProgress(fn ProgressFunc, stage string, done, total int64) {
	if fn == nil {
		return
	}
	fn(ProgressEvent{
		Stage:      stage,
		BytesDone:  done,
		BytesTotal: total,
	})
}

type countingWriter struct {
	w       io.Writer
	onDelta func(int64)
	written int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	if n > 0 {
		c.written += int64(n)
		if c.onDelta != nil {
			c.onDelta(int64(n))
		}
	}
	return n, err
}
