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
	RootDir      string
	OutBundle    string
	Recipient    string
	IdentityOut  string
}

type SnapshotSummary struct {
	ComposeFiles   int
	CapturedFiles  int
	ExternalFiles  int
	SkippedMissing int
	Manifest       manifest.Manifest
	GeneratedIdentityPath string
}

func CreateSnapshot(opts SnapshotOptions) (SnapshotSummary, error) {
	if runtime.GOOS != "linux" {
		return SnapshotSummary{}, fmt.Errorf("linux-only MVP: current platform is %s", runtime.GOOS)
	}
	if err := crypto.EnsureAgeInstalled(); err != nil {
		return SnapshotSummary{}, err
	}

	rootAbs, err := filepath.Abs(opts.RootDir)
	if err != nil {
		return SnapshotSummary{}, fmt.Errorf("resolve root path: %w", err)
	}
	st, err := os.Stat(rootAbs)
	if err != nil {
		return SnapshotSummary{}, fmt.Errorf("stat root path: %w", err)
	}
	if !st.IsDir() {
		return SnapshotSummary{}, fmt.Errorf("root path must be a directory")
	}

	composeFiles, err := discovery.DiscoverComposeFiles(rootAbs)
	if err != nil {
		return SnapshotSummary{}, err
	}
	if len(composeFiles) == 0 {
		return SnapshotSummary{}, fmt.Errorf("no compose files found under %s", rootAbs)
	}

	recipient := strings.TrimSpace(opts.Recipient)
	validationKeyPath := ""
	summary := SnapshotSummary{ComposeFiles: len(composeFiles)}
	if recipient == "" {
		if opts.IdentityOut == "" {
			opts.IdentityOut = "identity.key"
		}
		identityAbs, err := filepath.Abs(opts.IdentityOut)
		if err != nil {
			return SnapshotSummary{}, fmt.Errorf("resolve identity-out path: %w", err)
		}
		if _, err := os.Stat(identityAbs); err == nil {
			return SnapshotSummary{}, fmt.Errorf("identity key already exists: %s", identityAbs)
		} else if !os.IsNotExist(err) {
			return SnapshotSummary{}, fmt.Errorf("stat identity key path: %w", err)
		}
		recipient, err = crypto.GenerateIdentity(identityAbs)
		if err != nil {
			return SnapshotSummary{}, err
		}
		summary.GeneratedIdentityPath = identityAbs
		validationKeyPath = identityAbs
	}

	kindByPath := map[string]string{}
	for _, p := range composeFiles {
		kindByPath[p] = manifest.KindCompose
	}

	composeRewritesByPath := map[string][]manifest.PathReplacement{}
	skippedMissing := 0
	for _, composePath := range composeFiles {
		parsed, err := compose.ParseFile(composePath)
		if err != nil {
			return SnapshotSummary{}, fmt.Errorf("parse compose file %s: %w", composePath, err)
		}
		for _, m := range parsed.Mentions {
			if includeAsCapturedFile(m.Kind) {
				fi, err := os.Stat(m.ResolvedAbs)
				if err != nil {
					skippedMissing++
					continue
				}
				if !fi.Mode().IsRegular() {
					continue
				}
				entryKind := kindFromMention(m.Kind)
				if !pathmap.IsInsideRoot(rootAbs, m.ResolvedAbs) {
					entryKind = manifest.KindExternal
				}
				existing := kindByPath[m.ResolvedAbs]
				kindByPath[m.ResolvedAbs] = mergeEntryKind(existing, entryKind)
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

	filePaths := make([]string, 0, len(kindByPath))
	for p := range kindByPath {
		filePaths = append(filePaths, p)
	}
	sort.Strings(filePaths)

	entries := make([]manifest.Entry, 0, len(filePaths))
	outsideIDs := make([]string, 0)
	entryIDByPath := map[string]string{}
	for _, p := range filePaths {
		id := stableEntryID(p)
		if _, ok := entryIDByPath[p]; ok {
			return SnapshotSummary{}, fmt.Errorf("duplicate entry source path %s", p)
		}
		entryIDByPath[p] = id

		sha, err := fileSHA256(p)
		if err != nil {
			return SnapshotSummary{}, fmt.Errorf("hash %s: %w", p, err)
		}
		fi, err := os.Stat(p)
		if err != nil {
			return SnapshotSummary{}, fmt.Errorf("stat %s: %w", p, err)
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
		entries = append(entries, manifest.Entry{
			ID:             id,
			Kind:           kindByPath[p],
			SourceAbsPath:  p,
			SourceRelPath:  sourceRel,
			RestoreRelPath: restoreRel,
			SHA256:         sha,
			Mode:           fmt.Sprintf("%04o", fi.Mode().Perm()),
		})
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
		return SnapshotSummary{}, err
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
		return SnapshotSummary{}, fmt.Errorf("build manifest: %w", err)
	}

	stage, err := os.MkdirTemp("", "infrakey-snapshot-")
	if err != nil {
		return SnapshotSummary{}, fmt.Errorf("create snapshot temp dir: %w", err)
	}
	defer os.RemoveAll(stage)

	payloadDir := filepath.Join(stage, "payload")
	if err := os.MkdirAll(filepath.Join(payloadDir, "files"), 0o700); err != nil {
		return SnapshotSummary{}, fmt.Errorf("create payload dir: %w", err)
	}
	for _, e := range entries {
		src := e.SourceAbsPath
		dst := filepath.Join(payloadDir, "files", e.ID)
		if err := copyFile(src, dst); err != nil {
			return SnapshotSummary{}, fmt.Errorf("stage entry %s: %w", e.ID, err)
		}
	}
	if err := manifest.WriteToFile(filepath.Join(payloadDir, "manifest.pci.json"), mf); err != nil {
		return SnapshotSummary{}, err
	}

	tarPath := filepath.Join(stage, "payload.tar")
	if err := CreateDeterministicTar(payloadDir, tarPath); err != nil {
		return SnapshotSummary{}, err
	}

	outAbs, err := filepath.Abs(opts.OutBundle)
	if err != nil {
		return SnapshotSummary{}, fmt.Errorf("resolve output bundle path: %w", err)
	}
	if err := crypto.EncryptFile(tarPath, outAbs, recipient); err != nil {
		return SnapshotSummary{}, err
	}

	if validationKeyPath != "" {
		testDecryptPath := filepath.Join(stage, "validation.tar")
		if err := crypto.DecryptFile(outAbs, validationKeyPath, testDecryptPath); err != nil {
			return SnapshotSummary{}, fmt.Errorf("post-encryption validation failed: %w", err)
		}
	}

	summary.CapturedFiles = len(entries)
	summary.ExternalFiles = len(outsideIDs)
	summary.SkippedMissing = skippedMissing
	summary.Manifest = mf
	return summary, nil
}

func includeAsCapturedFile(mentionKind string) bool {
	switch mentionKind {
	case compose.MentionEnvFile, compose.MentionSecret, compose.MentionConfig, compose.MentionCert:
		return true
	default:
		return false
	}
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
	default:
		return manifest.KindExternal
	}
}

func mergeEntryKind(existing, next string) string {
	if existing == "" {
		return next
	}
	priority := map[string]int{
		manifest.KindCompose: 6,
		manifest.KindCert:    5,
		manifest.KindSecret:  4,
		manifest.KindConfig:  3,
		manifest.KindEnv:     2,
		manifest.KindExternal: 1,
	}
	if priority[next] > priority[existing] {
		return next
	}
	return existing
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
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read randomness: %w", err)
	}
	return hex.EncodeToString(b), nil
}
