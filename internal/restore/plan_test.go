package restore

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"infrakey/internal/bundle"
	"infrakey/internal/crypto"
)

func TestPlanRestoreNoWrites(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only restore logic")
	}
	if err := crypto.EnsureAgeInstalled(); err != nil {
		t.Skipf("age not installed: %v", err)
	}
	if err := crypto.EnsureAgeKeygenInstalled(); err != nil {
		t.Skipf("age-keygen not installed: %v", err)
	}

	root := t.TempDir()
	stack := filepath.Join(root, "stack")
	if err := os.MkdirAll(stack, 0o755); err != nil {
		t.Fatal(err)
	}
	composePath := filepath.Join(stack, "docker-compose.yml")
	compose := "services:\n  app:\n    image: alpine:latest\n    env_file:\n      - .env\n"
	if err := os.WriteFile(composePath, []byte(compose), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stack, ".env"), []byte("FOO=bar\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	bundleOut := filepath.Join(root, "vault.bundle")
	identityOut := filepath.Join(root, "identity.key")
	if _, err := bundle.CreateSnapshot(bundle.SnapshotOptions{
		RootDir:     root,
		OutBundle:   bundleOut,
		IdentityOut: identityOut,
	}); err != nil {
		t.Fatalf("CreateSnapshot failed: %v", err)
	}

	target := filepath.Join(root, "restore-target")
	plan, err := PlanRestore(Options{
		BundlePath:      bundleOut,
		IdentityKeyPath: identityOut,
		TargetDir:       target,
		Yes:             true,
		IncludeExternal: "none",
	})
	if err != nil {
		t.Fatalf("PlanRestore failed: %v", err)
	}
	if len(plan.RestoredEntries) == 0 {
		t.Fatalf("expected planned restored entries")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("expected target not to be written in dry-run")
	}
}
