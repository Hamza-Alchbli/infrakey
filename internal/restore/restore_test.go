package restore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"infrakey/internal/manifest"
	"infrakey/internal/pathmap"
)

func TestEnsureTargetEmptyOrAbsent(t *testing.T) {
	root := t.TempDir()
	nonEmpty := filepath.Join(root, "target")
	if err := os.MkdirAll(nonEmpty, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nonEmpty, "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureTargetEmptyOrAbsent(nonEmpty); err == nil {
		t.Fatalf("expected error for non-empty target")
	}

	absent := filepath.Join(root, "absent")
	if err := ensureTargetEmptyOrAbsent(absent); err != nil {
		t.Fatalf("expected absent target to be accepted, got %v", err)
	}
}

func TestDecideExternalInclusionYesWithoutPolicyFails(t *testing.T) {
	mf := manifest.Manifest{
		Entries: []manifest.Entry{{ID: "e1", SourceAbsPath: "/etc/app.env"}},
	}
	outside := map[string]struct{}{"e1": {}}
	_, err := decideExternalInclusion(mf, outside, Options{Yes: true})
	if err == nil {
		t.Fatalf("expected validation error")
	}
}

func TestParseMode(t *testing.T) {
	m, err := parseMode("0640")
	if err != nil {
		t.Fatalf("unexpected parseMode error: %v", err)
	}
	if m.Perm() != 0o640 {
		t.Fatalf("unexpected mode: %o", m.Perm())
	}
}

func TestApplyComposeRewrites_RewritesExactPathOnly(t *testing.T) {
	root := t.TempDir()
	stagingDir := filepath.Join(root, "staging")
	targetAbs := filepath.Join(root, "target")
	if err := os.MkdirAll(filepath.Join(stagingDir, "stack"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(targetAbs, 0o755); err != nil {
		t.Fatal(err)
	}

	originalPath := "/etc/app.env"
	composePath := filepath.Join(stagingDir, "stack", "docker-compose.yml")
	compose := strings.Join([]string{
		"services:",
		"  app:",
		"    env_file:",
		"      - /etc/app.env",
		"    labels:",
		"      backup: \"/etc/app.env.backup\"",
		"",
	}, "\n")
	if err := os.WriteFile(composePath, []byte(compose), 0o644); err != nil {
		t.Fatal(err)
	}

	mf := manifest.Manifest{
		ComposeRewrites: []manifest.ComposeRewrite{
			{
				ComposeEntryID: "compose-entry",
				Replacements: []manifest.PathReplacement{
					{
						OriginalPath: originalPath,
						RestoredPath: "._infrakey_external/abcd1234/app.env",
					},
				},
			},
		},
	}
	entryByID := map[string]manifest.Entry{
		"compose-entry": {
			ID:             "compose-entry",
			RestoreRelPath: "stack/docker-compose.yml",
		},
	}
	if err := applyComposeRewrites(stagingDir, targetAbs, mf, entryByID, map[string]struct{}{}); err != nil {
		t.Fatalf("applyComposeRewrites failed: %v", err)
	}

	rewritten, err := os.ReadFile(composePath)
	if err != nil {
		t.Fatal(err)
	}
	newPathAbs, err := pathmap.TargetPath(targetAbs, "._infrakey_external/abcd1234/app.env")
	if err != nil {
		t.Fatal(err)
	}
	newPath := filepath.ToSlash(newPathAbs)
	content := string(rewritten)

	if !strings.Contains(content, newPath) {
		t.Fatalf("expected rewritten compose to contain %q", newPath)
	}
	if !strings.Contains(content, "/etc/app.env.backup") {
		t.Fatalf("expected suffix path to stay unchanged, got:\n%s", content)
	}
}
