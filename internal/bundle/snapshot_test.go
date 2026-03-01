package bundle

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"infrakey/internal/manifest"
)

func TestPlanSnapshotNoWrites(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only snapshot logic")
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

	plan, err := PlanSnapshot(SnapshotOptions{
		RootDir:     root,
		OutBundle:   bundleOut,
		IdentityOut: identityOut,
	})
	if err != nil {
		t.Fatalf("PlanSnapshot failed: %v", err)
	}
	if !plan.WouldGenerateIdentity {
		t.Fatalf("expected WouldGenerateIdentity=true")
	}
	if len(plan.Entries) == 0 {
		t.Fatalf("expected planned entries")
	}
	if _, err := os.Stat(bundleOut); !os.IsNotExist(err) {
		t.Fatalf("expected no bundle written in dry-run")
	}
	if _, err := os.Stat(identityOut); !os.IsNotExist(err) {
		t.Fatalf("expected no identity key written in dry-run")
	}
}

func TestPlanSnapshotRespectsComposeSelection(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only snapshot logic")
	}
	root := t.TempDir()
	appA := filepath.Join(root, "immich")
	appB := filepath.Join(root, "vaultwarden")
	if err := os.MkdirAll(appA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(appB, 0o755); err != nil {
		t.Fatal(err)
	}

	composeA := filepath.Join(appA, "docker-compose.yml")
	composeB := filepath.Join(appB, "compose.yaml")
	if err := os.WriteFile(composeA, []byte("services:\n  app:\n    image: alpine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(composeB, []byte("services:\n  app:\n    image: alpine\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	plan, err := PlanSnapshot(SnapshotOptions{
		RootDir:      root,
		OutBundle:    filepath.Join(root, "vault.bundle"),
		IdentityOut:  filepath.Join(root, "identity.key"),
		ComposePaths: []string{composeA},
	})
	if err != nil {
		t.Fatalf("PlanSnapshot failed: %v", err)
	}
	if len(plan.ComposePaths) != 1 {
		t.Fatalf("expected 1 compose file, got %d", len(plan.ComposePaths))
	}
	if plan.ComposePaths[0] != composeA {
		t.Fatalf("unexpected compose file in plan: %s", plan.ComposePaths[0])
	}
	for _, e := range plan.Entries {
		if strings.Contains(e.RestoreRelPath, "vaultwarden") {
			t.Fatalf("unexpected entry from non-selected compose app: %s", e.RestoreRelPath)
		}
	}
}

func TestPlanSnapshotFullCopyIncludesVolumeDirectory(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only snapshot logic")
	}
	root := t.TempDir()
	stack := filepath.Join(root, "stack")
	if err := os.MkdirAll(filepath.Join(stack, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stack, "data", "blob.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	composePath := filepath.Join(stack, "docker-compose.yml")
	compose := "services:\n  app:\n    image: alpine\n    volumes:\n      - ./data:/data\n"
	if err := os.WriteFile(composePath, []byte(compose), 0o644); err != nil {
		t.Fatal(err)
	}

	plan, err := PlanSnapshot(SnapshotOptions{
		RootDir:     root,
		OutBundle:   filepath.Join(root, "vault.bundle"),
		IdentityOut: filepath.Join(root, "identity.key"),
		FullCopy:    true,
	})
	if err != nil {
		t.Fatalf("PlanSnapshot failed: %v", err)
	}

	foundDir := false
	for _, e := range plan.Entries {
		if strings.HasSuffix(e.RestoreRelPath, "/data") {
			if e.EffectiveEntryType() != "directory" {
				t.Fatalf("expected directory entry type, got %s", e.EffectiveEntryType())
			}
			if e.EffectiveDataFormat() != "tar_dir" {
				t.Fatalf("expected tar_dir data format, got %s", e.EffectiveDataFormat())
			}
			foundDir = true
		}
	}
	if !foundDir {
		t.Fatalf("expected data directory to be included in full-copy plan")
	}
}

func TestPlanSnapshotFullCopyIncludesComposeProjectDirectory(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only snapshot logic")
	}
	root := t.TempDir()
	stack := filepath.Join(root, "caddy")
	if err := os.MkdirAll(stack, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stack, "Caddyfile"), []byte(":80 {\n  respond \"ok\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	composePath := filepath.Join(stack, "docker-compose.yml")
	compose := "services:\n  caddy:\n    image: caddy:latest\n"
	if err := os.WriteFile(composePath, []byte(compose), 0o644); err != nil {
		t.Fatal(err)
	}

	plan, err := PlanSnapshot(SnapshotOptions{
		RootDir:     root,
		OutBundle:   filepath.Join(root, "vault.bundle"),
		IdentityOut: filepath.Join(root, "identity.key"),
		FullCopy:    true,
	})
	if err != nil {
		t.Fatalf("PlanSnapshot failed: %v", err)
	}

	foundProjectDir := false
	for _, e := range plan.Entries {
		if e.RestoreRelPath == "caddy" && e.EffectiveEntryType() == manifest.EntryTypeDirectory {
			foundProjectDir = true
		}
	}
	if !foundProjectDir {
		t.Fatalf("expected compose project directory to be included in full-copy plan")
	}
}
