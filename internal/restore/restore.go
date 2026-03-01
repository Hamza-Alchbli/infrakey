package restore

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"infrakey/internal/bundle"
	"infrakey/internal/crypto"
	"infrakey/internal/manifest"
	"infrakey/internal/pathmap"
	"infrakey/internal/prompt"
)

type Options struct {
	BundlePath      string
	IdentityKeyPath string
	TargetDir       string
	Yes             bool
	IncludeExternal string // "", "all", "none"
	Progress        ProgressFunc
}

type ProgressEvent struct {
	Stage      string
	BytesDone  int64
	BytesTotal int64
}

type ProgressFunc func(ProgressEvent)

type Summary struct {
	RestoredEntries int
	SkippedExternal int
	ExternalTotal   int
}

type Plan struct {
	TargetPath      string
	RestoredEntries []manifest.Entry
	SkippedExternal []manifest.Entry
	Manifest        manifest.Manifest
}

type preparedRestore struct {
	targetAbs       string
	payloadDir      string
	mf              manifest.Manifest
	entryByID       map[string]manifest.Entry
	outsideSet      map[string]struct{}
	includeExternal map[string]bool
	cleanup         func()
}

func validateOptions(opts Options) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("linux-only MVP: current platform is %s", runtime.GOOS)
	}
	if opts.Yes && opts.IncludeExternal == "" {
		return fmt.Errorf("--yes requires --include-external all|none")
	}
	if opts.IncludeExternal != "" && opts.IncludeExternal != "all" && opts.IncludeExternal != "none" {
		return fmt.Errorf("invalid --include-external value %q (expected all or none)", opts.IncludeExternal)
	}
	if opts.BundlePath == "" || opts.IdentityKeyPath == "" || opts.TargetDir == "" {
		return fmt.Errorf("bundle, identity key and target are required")
	}
	return nil
}

func prepareRestore(opts Options) (preparedRestore, error) {
	var prep preparedRestore
	if err := validateOptions(opts); err != nil {
		return prep, err
	}

	targetAbs, err := filepath.Abs(opts.TargetDir)
	if err != nil {
		return prep, fmt.Errorf("resolve target path: %w", err)
	}
	if err := ensureTargetEmptyOrAbsent(targetAbs); err != nil {
		return prep, err
	}

	workDir, err := os.MkdirTemp("", "infrakey-restore-")
	if err != nil {
		return prep, fmt.Errorf("create temp dir: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(workDir) }
	cleanupNeeded := true
	defer func() {
		if cleanupNeeded {
			cleanup()
		}
	}()

	bundleReader, _, err := bundle.OpenBundleReader(opts.BundlePath)
	if err != nil {
		return prep, err
	}
	defer bundleReader.Close()

	payloadDir := filepath.Join(workDir, "payload")
	if err := os.MkdirAll(payloadDir, 0o700); err != nil {
		return prep, fmt.Errorf("create payload dir: %w", err)
	}
	reportRestoreProgress(opts.Progress, "decrypting", 0, 0)
	extractDone := int64(0)
	reportRestoreProgress(opts.Progress, "extracting", extractDone, 0)
	if err := crypto.DecryptFromReader(bundleReader, opts.IdentityKeyPath, func(r io.Reader) error {
		return bundle.ExtractTarReaderWithProgress(r, payloadDir, func(n int64) {
			extractDone += n
			reportRestoreProgress(opts.Progress, "extracting", extractDone, 0)
		})
	}); err != nil {
		return prep, fmt.Errorf("extract payload: %w", err)
	}

	manifestPath := filepath.Join(payloadDir, "manifest.pci.json")
	mf, err := manifest.ReadFromFile(manifestPath)
	if err != nil {
		return prep, fmt.Errorf("load manifest: %w", err)
	}

	entryByID := make(map[string]manifest.Entry, len(mf.Entries))
	for _, e := range mf.Entries {
		entryByID[e.ID] = e
	}

	if err := validateManifestPaths(mf); err != nil {
		return prep, err
	}

	outsideSet := mf.OutsideRootSet()
	includeExternal, err := decideExternalInclusion(mf, outsideSet, opts)
	if err != nil {
		return prep, err
	}

	prep = preparedRestore{
		targetAbs:       targetAbs,
		payloadDir:      payloadDir,
		mf:              mf,
		entryByID:       entryByID,
		outsideSet:      outsideSet,
		includeExternal: includeExternal,
		cleanup:         cleanup,
	}
	cleanupNeeded = false
	return prep, nil
}

func PlanRestore(opts Options) (Plan, error) {
	prep, err := prepareRestore(opts)
	if err != nil {
		return Plan{}, err
	}
	defer prep.cleanup()

	plan := Plan{
		TargetPath: prep.targetAbs,
		Manifest:   prep.mf,
	}
	for _, e := range prep.mf.Entries {
		if _, outside := prep.outsideSet[e.ID]; outside && !prep.includeExternal[e.ID] {
			plan.SkippedExternal = append(plan.SkippedExternal, e)
			continue
		}
		plan.RestoredEntries = append(plan.RestoredEntries, e)
	}
	return plan, nil
}

func Run(opts Options) (Summary, error) {
	prep, err := prepareRestore(opts)
	if err != nil {
		return Summary{}, err
	}
	defer prep.cleanup()

	stagingDir, err := createStagingDir(prep.targetAbs)
	if err != nil {
		return Summary{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(stagingDir)
		}
	}()

	skippedSources := map[string]struct{}{}
	summary := Summary{ExternalTotal: len(prep.outsideSet)}
	totalBytes := int64(0)
	for _, e := range prep.mf.Entries {
		if _, outside := prep.outsideSet[e.ID]; outside && !prep.includeExternal[e.ID] {
			continue
		}
		src := filepath.Join(prep.payloadDir, "files", e.ID)
		if fi, err := os.Stat(src); err == nil && fi.Mode().IsRegular() {
			totalBytes += fi.Size()
		}
	}
	doneBytes := int64(0)
	reportRestoreProgress(opts.Progress, "restoring", doneBytes, totalBytes)
	for _, e := range prep.mf.Entries {
		if _, outside := prep.outsideSet[e.ID]; outside && !prep.includeExternal[e.ID] {
			summary.SkippedExternal++
			skippedSources[e.SourceAbsPath] = struct{}{}
			continue
		}
		src := filepath.Join(prep.payloadDir, "files", e.ID)
		dst, err := pathmap.TargetPath(stagingDir, e.RestoreRelPath)
		if err != nil {
			return Summary{}, fmt.Errorf("resolve restore path for entry %q: %w", e.ID, err)
		}
		mode, err := parseMode(e.Mode)
		if err != nil {
			return Summary{}, fmt.Errorf("invalid mode for entry %q: %w", e.ID, err)
		}

		switch e.EffectiveDataFormat() {
		case manifest.DataFormatRaw:
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return Summary{}, fmt.Errorf("mkdir restore dir for %q: %w", e.ID, err)
			}
			_, sum, err := copyFileWithProgressAndHash(src, dst, func(n int64) {
				doneBytes += n
				reportRestoreProgress(opts.Progress, "restoring", doneBytes, totalBytes)
			})
			if err != nil {
				return Summary{}, fmt.Errorf("restore entry %q: %w", e.ID, err)
			}
			if sum != e.SHA256 {
				return Summary{}, fmt.Errorf("checksum mismatch for entry %q", e.ID)
			}
			if err := os.Chmod(dst, mode); err != nil {
				return Summary{}, fmt.Errorf("chmod restored entry %q: %w", e.ID, err)
			}
		case manifest.DataFormatTarDir:
			if err := os.MkdirAll(dst, mode); err != nil {
				return Summary{}, fmt.Errorf("mkdir restore directory for %q: %w", e.ID, err)
			}
			sum, err := extractTarFileWithProgressAndHash(src, dst, func(n int64) {
				doneBytes += n
				reportRestoreProgress(opts.Progress, "restoring", doneBytes, totalBytes)
			})
			if err != nil {
				return Summary{}, fmt.Errorf("restore directory entry %q: %w", e.ID, err)
			}
			if sum != e.SHA256 {
				return Summary{}, fmt.Errorf("checksum mismatch for directory entry %q", e.ID)
			}
			if err := os.Chmod(dst, mode); err != nil {
				return Summary{}, fmt.Errorf("chmod restored directory %q: %w", e.ID, err)
			}
		default:
			return Summary{}, fmt.Errorf("unsupported data format for entry %q: %s", e.ID, e.DataFormat)
		}
		summary.RestoredEntries++
	}

	if err := applyComposeRewrites(stagingDir, prep.targetAbs, prep.mf, prep.entryByID, skippedSources); err != nil {
		return Summary{}, err
	}

	if err := commitStaging(stagingDir, prep.targetAbs); err != nil {
		return Summary{}, err
	}
	committed = true

	return summary, nil
}

func validateManifestPaths(mf manifest.Manifest) error {
	seenRestore := map[string]struct{}{}
	for _, e := range mf.Entries {
		if _, ok := seenRestore[e.RestoreRelPath]; ok {
			return fmt.Errorf("manifest has duplicate restoreRelPath %q", e.RestoreRelPath)
		}
		seenRestore[e.RestoreRelPath] = struct{}{}
	}
	return nil
}

func decideExternalInclusion(mf manifest.Manifest, outsideSet map[string]struct{}, opts Options) (map[string]bool, error) {
	include := make(map[string]bool, len(outsideSet))
	if len(outsideSet) == 0 {
		return include, nil
	}

	switch opts.IncludeExternal {
	case "all":
		for id := range outsideSet {
			include[id] = true
		}
		return include, nil
	case "none":
		for id := range outsideSet {
			include[id] = false
		}
		return include, nil
	}

	if opts.Yes {
		return nil, fmt.Errorf("--yes requires --include-external all|none")
	}

	type externalEntry struct {
		ID   string
		Path string
	}
	entries := make([]externalEntry, 0, len(outsideSet))
	entryByID := map[string]manifest.Entry{}
	for _, e := range mf.Entries {
		entryByID[e.ID] = e
	}
	for id := range outsideSet {
		entries = append(entries, externalEntry{ID: id, Path: entryByID[id].SourceAbsPath})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })

	for _, ee := range entries {
		ok, err := prompt.Confirm(fmt.Sprintf("Include external reference %s", ee.Path))
		if err != nil {
			return nil, fmt.Errorf("read prompt input: %w", err)
		}
		include[ee.ID] = ok
	}
	return include, nil
}

func applyComposeRewrites(stagingDir, targetAbs string, mf manifest.Manifest, entryByID map[string]manifest.Entry, skippedSources map[string]struct{}) error {
	for _, rw := range mf.ComposeRewrites {
		entry, ok := entryByID[rw.ComposeEntryID]
		if !ok {
			return fmt.Errorf("compose rewrite references unknown composeEntryId %q", rw.ComposeEntryID)
		}
		composePath, err := pathmap.TargetPath(stagingDir, entry.RestoreRelPath)
		if err != nil {
			return fmt.Errorf("resolve compose restore path %q: %w", rw.ComposeEntryID, err)
		}
		b, err := os.ReadFile(composePath)
		if err != nil {
			return fmt.Errorf("read restored compose file %q: %w", composePath, err)
		}
		content := string(b)
		for _, repl := range rw.Replacements {
			if _, skipped := skippedSources[repl.OriginalPath]; skipped {
				continue
			}
			newPathAbs, err := pathmap.TargetPath(targetAbs, repl.RestoredPath)
			if err != nil {
				return fmt.Errorf("resolve compose rewrite restored path %q: %w", repl.RestoredPath, err)
			}
			content = strings.ReplaceAll(content, repl.OriginalPath, filepath.ToSlash(newPathAbs))
		}
		if err := os.WriteFile(composePath, []byte(content), 0o600); err != nil {
			return fmt.Errorf("write rewritten compose file %q: %w", composePath, err)
		}
	}
	return nil
}

func ensureTargetEmptyOrAbsent(targetAbs string) error {
	st, err := os.Stat(targetAbs)
	if err != nil {
		if os.IsNotExist(err) {
			if err := os.MkdirAll(filepath.Dir(targetAbs), 0o755); err != nil {
				return fmt.Errorf("create target parent: %w", err)
			}
			return nil
		}
		return fmt.Errorf("stat target: %w", err)
	}
	if !st.IsDir() {
		return fmt.Errorf("restore target exists and is not a directory")
	}
	entries, err := os.ReadDir(targetAbs)
	if err != nil {
		return fmt.Errorf("read target dir: %w", err)
	}
	if len(entries) > 0 {
		return fmt.Errorf("restore target must be empty or absent")
	}
	return nil
}

func createStagingDir(targetAbs string) (string, error) {
	parent := filepath.Dir(targetAbs)
	randSuffix, err := randomHex(6)
	if err != nil {
		return "", err
	}
	stage := filepath.Join(parent, ".infrakey-staging-"+randSuffix)
	// Keep target root traversable after atomic rename (common sudo restore case).
	if err := os.MkdirAll(stage, 0o755); err != nil {
		return "", fmt.Errorf("create staging dir: %w", err)
	}
	return stage, nil
}

func commitStaging(stagingDir, targetAbs string) error {
	if _, err := os.Stat(targetAbs); err == nil {
		if err := os.Remove(targetAbs); err != nil {
			return fmt.Errorf("remove empty target dir before commit: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat target before commit: %w", err)
	}
	if err := os.Rename(stagingDir, targetAbs); err != nil {
		return fmt.Errorf("atomic commit rename failed: %w", err)
	}
	return nil
}

func copyFile(src, dst string) error {
	_, _, err := copyFileWithProgressAndHash(src, dst, nil)
	return err
}

func copyFileWithProgress(src, dst string, onDelta func(int64)) (int64, error) {
	written, _, err := copyFileWithProgressAndHash(src, dst, onDelta)
	return written, err
}

func copyFileWithProgressAndHash(src, dst string, onDelta func(int64)) (int64, string, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, "", err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, "", err
	}
	hasher := sha256.New()
	w := io.MultiWriter(out, hasher)
	buf := make([]byte, 1024*1024)
	written, err := io.CopyBuffer(w, in, buf)
	if err != nil {
		out.Close()
		return 0, "", err
	}
	if onDelta != nil && written > 0 {
		onDelta(written)
	}
	if err := out.Close(); err != nil {
		return written, "", err
	}
	return written, hex.EncodeToString(hasher.Sum(nil)), nil
}

func extractTarFileWithProgressAndHash(src, dst string, onDelta func(int64)) (string, error) {
	f, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer f.Close()
	hasher := sha256.New()
	tee := io.TeeReader(f, hasher)
	if err := bundle.ExtractTarReaderWithProgress(tee, dst, onDelta); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func parseMode(mode string) (os.FileMode, error) {
	m, err := strconv.ParseUint(mode, 8, 32)
	if err != nil {
		return 0, err
	}
	return os.FileMode(m), nil
}

func randomHex(bytesN int) (string, error) {
	b := make([]byte, bytesN)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read randomness: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func reportRestoreProgress(fn ProgressFunc, stage string, done, total int64) {
	if fn == nil {
		return
	}
	fn(ProgressEvent{
		Stage:      stage,
		BytesDone:  done,
		BytesTotal: total,
	})
}
