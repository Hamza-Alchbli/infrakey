package restore

import (
	"os"
	"path/filepath"
	"testing"

	"infrakey/internal/manifest"
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
