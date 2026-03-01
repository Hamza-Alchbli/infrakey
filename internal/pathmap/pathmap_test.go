package pathmap

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestComputeRestoreRelPath_InRoot(t *testing.T) {
	root := filepath.Clean("/srv/apps")
	source := filepath.Clean("/srv/apps/project/.env")
	rel, inRoot := ComputeRestoreRelPath(root, source)
	if !inRoot {
		t.Fatalf("expected inRoot=true")
	}
	if rel != "project/.env" {
		t.Fatalf("unexpected rel path: %s", rel)
	}
}

func TestComputeRestoreRelPath_External(t *testing.T) {
	root := filepath.Clean("/srv/apps")
	source := filepath.Clean("/etc/ssl/cert.pem")
	rel, inRoot := ComputeRestoreRelPath(root, source)
	if inRoot {
		t.Fatalf("expected external path")
	}
	if rel == "" || !strings.HasPrefix(rel, ExternalBaseDir) {
		t.Fatalf("unexpected external rel path: %s", rel)
	}
}

func TestTargetPathRejectsEscape(t *testing.T) {
	_, err := TargetPath("/tmp/restore", "../etc/passwd")
	if err == nil {
		t.Fatalf("expected path escape error")
	}
}
