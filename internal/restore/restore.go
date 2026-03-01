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
	BundlePath       string
	IdentityKeyPath  string
	TargetDir        string
	Yes              bool
	IncludeExternal  string // "", "all", "none"
}

type Summary struct {
	RestoredEntries int
	SkippedExternal int
	ExternalTotal   int
}

func Run(opts Options) (Summary, error) {
	if runtime.GOOS != "linux" {
		return Summary{}, fmt.Errorf("linux-only MVP: current platform is %s", runtime.GOOS)
	}
	if opts.Yes && opts.IncludeExternal == "" {
		return Summary{}, fmt.Errorf("--yes requires --include-external all|none")
	}
	if opts.IncludeExternal != "" && opts.IncludeExternal != "all" && opts.IncludeExternal != "none" {
		return Summary{}, fmt.Errorf("invalid --include-external value %q (expected all or none)", opts.IncludeExternal)
	}

	targetAbs, err := filepath.Abs(opts.TargetDir)
	if err != nil {
		return Summary{}, fmt.Errorf("resolve target path: %w", err)
	}
	if err := ensureTargetEmptyOrAbsent(targetAbs); err != nil {
		return Summary{}, err
	}

	workDir, err := os.MkdirTemp("", "infrakey-restore-")
	if err != nil {
		return Summary{}, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(workDir)

	decryptedTar := filepath.Join(workDir, "payload.tar")
	if err := crypto.DecryptFile(opts.BundlePath, opts.IdentityKeyPath, decryptedTar); err != nil {
		return Summary{}, err
	}

	payloadDir := filepath.Join(workDir, "payload")
	if err := os.MkdirAll(payloadDir, 0o700); err != nil {
		return Summary{}, fmt.Errorf("create payload dir: %w", err)
	}
	if err := bundle.ExtractTar(decryptedTar, payloadDir); err != nil {
		return Summary{}, fmt.Errorf("extract payload: %w", err)
	}

	manifestPath := filepath.Join(payloadDir, "manifest.pci.json")
	mf, err := manifest.ReadFromFile(manifestPath)
	if err != nil {
		return Summary{}, fmt.Errorf("load manifest: %w", err)
	}

	entryByID := make(map[string]manifest.Entry, len(mf.Entries))
	for _, e := range mf.Entries {
		entryByID[e.ID] = e
	}

	if err := validatePayloadFiles(payloadDir, mf); err != nil {
		return Summary{}, err
	}

	outsideSet := mf.OutsideRootSet()
	includeExternal, err := decideExternalInclusion(mf, outsideSet, opts)
	if err != nil {
		return Summary{}, err
	}

	stagingDir, err := createStagingDir(targetAbs)
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
	summary := Summary{ExternalTotal: len(outsideSet)}
	for _, e := range mf.Entries {
		if _, outside := outsideSet[e.ID]; outside && !includeExternal[e.ID] {
			summary.SkippedExternal++
			skippedSources[e.SourceAbsPath] = struct{}{}
			continue
		}
		src := filepath.Join(payloadDir, "files", e.ID)
		dst, err := pathmap.TargetPath(stagingDir, e.RestoreRelPath)
		if err != nil {
			return Summary{}, fmt.Errorf("resolve restore path for entry %q: %w", e.ID, err)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return Summary{}, fmt.Errorf("mkdir restore dir for %q: %w", e.ID, err)
		}
		if err := copyFile(src, dst); err != nil {
			return Summary{}, fmt.Errorf("restore entry %q: %w", e.ID, err)
		}
		mode, err := parseMode(e.Mode)
		if err != nil {
			return Summary{}, fmt.Errorf("invalid mode for entry %q: %w", e.ID, err)
		}
		if err := os.Chmod(dst, mode); err != nil {
			return Summary{}, fmt.Errorf("chmod restored entry %q: %w", e.ID, err)
		}
		summary.RestoredEntries++
	}

	if err := applyComposeRewrites(stagingDir, targetAbs, mf, entryByID, skippedSources); err != nil {
		return Summary{}, err
	}

	if err := commitStaging(stagingDir, targetAbs); err != nil {
		return Summary{}, err
	}
	committed = true

	return summary, nil
}

func validatePayloadFiles(payloadDir string, mf manifest.Manifest) error {
	seenRestore := map[string]struct{}{}
	for _, e := range mf.Entries {
		if _, ok := seenRestore[e.RestoreRelPath]; ok {
			return fmt.Errorf("manifest has duplicate restoreRelPath %q", e.RestoreRelPath)
		}
		seenRestore[e.RestoreRelPath] = struct{}{}
		src := filepath.Join(payloadDir, "files", e.ID)
		sum, err := fileSHA256(src)
		if err != nil {
			return fmt.Errorf("hash payload entry %q: %w", e.ID, err)
		}
		if sum != e.SHA256 {
			return fmt.Errorf("checksum mismatch for entry %q", e.ID)
		}
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
	if err := os.MkdirAll(stage, 0o700); err != nil {
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
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
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
