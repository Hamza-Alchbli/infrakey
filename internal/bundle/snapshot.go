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
	ComposePaths []string
}

type SnapshotSummary struct {
	ComposeFiles          int
	CapturedFiles         int
	ExternalFiles         int
	SkippedMissing        int
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

	kindByPath := map[string]string{}
	for _, p := range composeFiles {
		kindByPath[p] = manifest.KindCompose
	}

	composeRewritesByPath := map[string][]manifest.PathReplacement{}
	skippedMissing := 0
	for _, composePath := range composeFiles {
		parsed, err := compose.ParseFile(composePath)
		if err != nil {
			return result, fmt.Errorf("parse compose file %s: %w", composePath, err)
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
			return result, fmt.Errorf("duplicate entry source path %s", p)
		}
		entryIDByPath[p] = id

		sha, err := fileSHA256(p)
		if err != nil {
			return result, fmt.Errorf("hash %s: %w", p, err)
		}
		fi, err := os.Stat(p)
		if err != nil {
			return result, fmt.Errorf("stat %s: %w", p, err)
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

	stage, err := os.MkdirTemp("", "infrakey-snapshot-")
	if err != nil {
		return SnapshotSummary{}, fmt.Errorf("create snapshot temp dir: %w", err)
	}
	defer os.RemoveAll(stage)

	payloadDir := filepath.Join(stage, "payload")
	if err := os.MkdirAll(filepath.Join(payloadDir, "files"), 0o700); err != nil {
		return SnapshotSummary{}, fmt.Errorf("create payload dir: %w", err)
	}
	for _, e := range result.entries {
		src := e.SourceAbsPath
		dst := filepath.Join(payloadDir, "files", e.ID)
		if err := copyFile(src, dst); err != nil {
			return SnapshotSummary{}, fmt.Errorf("stage entry %s: %w", e.ID, err)
		}
	}
	if err := manifest.WriteToFile(filepath.Join(payloadDir, "manifest.pci.json"), result.summary.Manifest); err != nil {
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
	if err := crypto.EncryptFile(tarPath, outAbs, result.recipient); err != nil {
		return SnapshotSummary{}, err
	}

	if result.validationKeyPath != "" {
		testDecryptPath := filepath.Join(stage, "validation.tar")
		if err := crypto.DecryptFile(outAbs, result.validationKeyPath, testDecryptPath); err != nil {
			return SnapshotSummary{}, fmt.Errorf("post-encryption validation failed: %w", err)
		}
	}

	return result.summary, nil
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
		manifest.KindCompose:  6,
		manifest.KindCert:     5,
		manifest.KindSecret:   4,
		manifest.KindConfig:   3,
		manifest.KindEnv:      2,
		manifest.KindExternal: 1,
	}
	if priority[next] > priority[existing] {
		return next
	}
	return existing
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
