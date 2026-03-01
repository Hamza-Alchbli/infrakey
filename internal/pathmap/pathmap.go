package pathmap

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
)

const ExternalBaseDir = "._infrakey_external"

func IsInsideRoot(root, candidate string) bool {
	rootClean := filepath.Clean(root)
	candidateClean := filepath.Clean(candidate)
	rel, err := filepath.Rel(rootClean, candidateClean)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func ComputeRestoreRelPath(root, sourceAbs string) (restoreRelPath string, inRoot bool) {
	if IsInsideRoot(root, sourceAbs) {
		rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(sourceAbs))
		if err == nil {
			return filepath.ToSlash(rel), true
		}
	}
	return ExternalRestoreRelPath(sourceAbs), false
}

func ExternalRestoreRelPath(sourceAbs string) string {
	dir := filepath.Dir(filepath.Clean(sourceAbs))
	h := sha1.Sum([]byte(dir))
	tag := hex.EncodeToString(h[:])[:12]
	base := filepath.Base(sourceAbs)
	if base == "." || base == string(filepath.Separator) || base == "" {
		base = "root"
	}
	return filepath.ToSlash(filepath.Join(ExternalBaseDir, tag, base))
}

func TargetPath(targetRoot, restoreRelPath string) (string, error) {
	if restoreRelPath == "" {
		return "", fmt.Errorf("empty restoreRelPath")
	}
	cleanRel := filepath.Clean(filepath.FromSlash(restoreRelPath))
	if cleanRel == "." || cleanRel == string(filepath.Separator) {
		return "", fmt.Errorf("invalid restoreRelPath %q", restoreRelPath)
	}
	if strings.HasPrefix(cleanRel, ".."+string(filepath.Separator)) || cleanRel == ".." {
		return "", fmt.Errorf("restoreRelPath escapes target: %q", restoreRelPath)
	}
	joined := filepath.Join(filepath.Clean(targetRoot), cleanRel)
	if !IsInsideRoot(filepath.Clean(targetRoot), joined) {
		return "", fmt.Errorf("resolved restore path escapes target: %q", restoreRelPath)
	}
	return joined, nil
}
