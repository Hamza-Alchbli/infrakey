package restore

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"infrakey/internal/bundle"
	"infrakey/internal/crypto"
	"infrakey/internal/pathmap"
)

func TestSnapshotInspectRestoreRoundTrip(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only snapshot/restore runtime")
	}
	if err := crypto.EnsureAgeInstalled(); err != nil {
		t.Skipf("age not installed: %v", err)
	}
	if err := crypto.EnsureAgeKeygenInstalled(); err != nil {
		t.Skipf("age-keygen not installed: %v", err)
	}

	root := t.TempDir()
	sourceRoot := filepath.Join(root, "source")
	stackDir := filepath.Join(sourceRoot, "stack")
	if err := os.MkdirAll(stackDir, 0o755); err != nil {
		t.Fatal(err)
	}
	externalDir := filepath.Join(root, "external")
	if err := os.MkdirAll(externalDir, 0o755); err != nil {
		t.Fatal(err)
	}

	localEnv := filepath.Join(stackDir, ".env")
	externalEnv := filepath.Join(externalDir, "outside.env")
	if err := os.WriteFile(localEnv, []byte("LOCAL=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(externalEnv, []byte("OUTSIDE=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	composePath := filepath.Join(stackDir, "docker-compose.yml")
	compose := strings.Join([]string{
		"services:",
		"  app:",
		"    image: alpine:latest",
		"    env_file:",
		"      - ./.env",
		"      - " + filepath.ToSlash(externalEnv),
		"",
	}, "\n")
	if err := os.WriteFile(composePath, []byte(compose), 0o644); err != nil {
		t.Fatal(err)
	}

	bundlePath := filepath.Join(root, "vault.bundle")
	identityPath := filepath.Join(root, "identity.key")
	snapshot, err := bundle.CreateSnapshot(bundle.SnapshotOptions{
		RootDir:     sourceRoot,
		OutBundle:   bundlePath,
		IdentityOut: identityPath,
	})
	if err != nil {
		t.Fatalf("CreateSnapshot failed: %v", err)
	}
	if snapshot.ExternalFiles == 0 {
		t.Fatalf("expected external file to be captured")
	}

	inspect, err := bundle.Inspect(bundle.InspectOptions{
		BundlePath:      bundlePath,
		IdentityKeyPath: identityPath,
	})
	if err != nil {
		t.Fatalf("Inspect failed: %v", err)
	}
	if len(inspect.Entries) == 0 {
		t.Fatalf("expected inspect entries")
	}
	if len(inspect.External) == 0 {
		t.Fatalf("expected inspect external entries")
	}

	restoreTarget := filepath.Join(root, "restored")
	restoreSummary, err := Run(Options{
		BundlePath:      bundlePath,
		IdentityKeyPath: identityPath,
		TargetDir:       restoreTarget,
		Yes:             true,
		IncludeExternal: "all",
	})
	if err != nil {
		t.Fatalf("Run restore failed: %v", err)
	}
	if restoreSummary.RestoredEntries == 0 {
		t.Fatalf("expected restored entries")
	}
	if restoreSummary.SkippedExternal != 0 {
		t.Fatalf("expected no skipped external entries, got %d", restoreSummary.SkippedExternal)
	}

	restoredCompose := filepath.Join(restoreTarget, "stack", "docker-compose.yml")
	composeBytes, err := os.ReadFile(restoredCompose)
	if err != nil {
		t.Fatalf("read restored compose: %v", err)
	}

	externalRestoreRel, inRoot := pathmap.ComputeRestoreRelPath(sourceRoot, externalEnv)
	if inRoot {
		t.Fatalf("expected external file to be mapped outside root")
	}
	externalRestoreAbs, err := pathmap.TargetPath(restoreTarget, externalRestoreRel)
	if err != nil {
		t.Fatalf("resolve external restored path: %v", err)
	}
	if _, err := os.Stat(externalRestoreAbs); err != nil {
		t.Fatalf("expected restored external file at %s: %v", externalRestoreAbs, err)
	}

	rewrittenCompose := string(composeBytes)
	if !strings.Contains(rewrittenCompose, filepath.ToSlash(externalRestoreAbs)) {
		t.Fatalf("expected compose to reference restored external path, got:\n%s", rewrittenCompose)
	}
	if strings.Contains(rewrittenCompose, filepath.ToSlash(externalEnv)) {
		t.Fatalf("expected compose not to reference original external path, got:\n%s", rewrittenCompose)
	}
}
