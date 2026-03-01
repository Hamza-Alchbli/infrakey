package bundle

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSplitAndResolveChunkedBundle(t *testing.T) {
	root := t.TempDir()
	bundlePath := filepath.Join(root, "vault.bundle")

	content := bytes.Repeat([]byte("abcdef0123456789"), 512) // 8 KiB
	if err := os.WriteFile(bundlePath, content, 0o600); err != nil {
		t.Fatal(err)
	}

	parts, err := SplitBundleIntoChunks(bundlePath, 2048)
	if err != nil {
		t.Fatalf("SplitBundleIntoChunks failed: %v", err)
	}
	if len(parts) < 2 {
		t.Fatalf("expected multiple chunk parts, got %d", len(parts))
	}
	chunkDir := bundlePath + ".parts"
	if st, err := os.Stat(chunkDir); err != nil || !st.IsDir() {
		t.Fatalf("expected chunk directory %s", chunkDir)
	}
	for _, p := range parts {
		if filepath.Dir(p.Path) != chunkDir {
			t.Fatalf("expected chunk %s to be inside %s", p.Path, chunkDir)
		}
	}
	if _, err := os.Stat(bundlePath); !os.IsNotExist(err) {
		t.Fatalf("expected original bundle removed after chunking")
	}

	work := filepath.Join(root, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveBundleInputPath(bundlePath, work)
	if err != nil {
		t.Fatalf("ResolveBundleInputPath failed: %v", err)
	}
	rebuilt, err := os.ReadFile(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rebuilt, content) {
		t.Fatalf("joined bundle content mismatch")
	}
}

func TestResolveRejectsLegacyChunkPattern(t *testing.T) {
	root := t.TempDir()
	bundlePath := filepath.Join(root, "vault.bundle")
	content := bytes.Repeat([]byte("legacy0123456789"), 400)

	part1 := append([]byte(nil), content[:2000]...)
	part2 := append([]byte(nil), content[2000:]...)
	if err := os.WriteFile(bundlePath+".part0001", part1, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bundlePath+".part0002", part2, 0o600); err != nil {
		t.Fatal(err)
	}

	work := filepath.Join(root, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := ResolveBundleInputPath(bundlePath, work)
	if err == nil {
		t.Fatalf("expected legacy chunk pattern to be rejected")
	}
	if !strings.Contains(err.Error(), "expected chunks in") {
		t.Fatalf("unexpected error for legacy chunk pattern: %v", err)
	}
}
