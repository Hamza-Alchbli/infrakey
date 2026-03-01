package discovery

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverComposeFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "node_modules", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "app", "docker-compose.yml"), []byte("services:{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "node_modules", "x", "compose.yaml"), []byte("services:{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := DiscoverComposeFiles(root)
	if err != nil {
		t.Fatalf("DiscoverComposeFiles error: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 compose file, got %d", len(files))
	}
	if filepath.Base(files[0]) != "docker-compose.yml" {
		t.Fatalf("unexpected compose file: %s", files[0])
	}
}
