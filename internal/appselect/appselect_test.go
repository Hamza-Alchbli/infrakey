package appselect

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestDiscoverAppEstimates(t *testing.T) {
	root := t.TempDir()
	appA := filepath.Join(root, "immich")
	appB := filepath.Join(root, "vaultwarden")
	if err := os.MkdirAll(appA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(appB, 0o755); err != nil {
		t.Fatal(err)
	}

	sharedSecret := filepath.Join(root, "shared.env")
	if err := os.WriteFile(sharedSecret, []byte("A=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	immichEnv := filepath.Join(appA, ".env")
	if err := os.WriteFile(immichEnv, []byte("IMMICH=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	composeA := "services:\n  s:\n    image: alpine\n    env_file:\n      - .env\n      - ../shared.env\n"
	composeB := "services:\n  s:\n    image: alpine\n    env_file:\n      - ../shared.env\n"
	pathA := filepath.Join(appA, "docker-compose.yml")
	pathB := filepath.Join(appB, "compose.yaml")
	if err := os.WriteFile(pathA, []byte(composeA), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pathB, []byte(composeB), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Discover(root, Options{})
	if err != nil {
		t.Fatalf("Discover error: %v", err)
	}
	if len(got.Apps) != 2 {
		t.Fatalf("expected 2 apps, got %d", len(got.Apps))
	}

	byName := map[string]App{}
	for _, app := range got.Apps {
		byName[app.Name] = app
	}
	if _, ok := byName["immich"]; !ok {
		t.Fatalf("missing immich app label")
	}
	if _, ok := byName["vaultwarden"]; !ok {
		t.Fatalf("missing vaultwarden app label")
	}

	// immich app estimate includes compose + immich env + shared env
	immichExpected := fileSize(t, pathA) + fileSize(t, immichEnv) + fileSize(t, sharedSecret)
	if byName["immich"].EstimatedSizeBytes != immichExpected {
		t.Fatalf("immich estimate mismatch: got %d want %d", byName["immich"].EstimatedSizeBytes, immichExpected)
	}

	// total estimate is unique across all files (composeA + composeB + immich env + shared env)
	totalExpected := fileSize(t, pathA) + fileSize(t, pathB) + fileSize(t, immichEnv) + fileSize(t, sharedSecret)
	if got.TotalEstimatedSizeBytes != totalExpected {
		t.Fatalf("total estimate mismatch: got %d want %d", got.TotalEstimatedSizeBytes, totalExpected)
	}
}

func TestDiscoverIncludesVolumeDirWhenRequested(t *testing.T) {
	root := t.TempDir()
	appDir := filepath.Join(root, "immich")
	if err := os.MkdirAll(filepath.Join(appDir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "data", "blob.bin"), make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	compose := "services:\n  app:\n    image: alpine\n    volumes:\n      - ./data:/data\n"
	composePath := filepath.Join(appDir, "docker-compose.yml")
	if err := os.WriteFile(composePath, []byte(compose), 0o644); err != nil {
		t.Fatal(err)
	}

	withoutVolumes, err := Discover(root, Options{IncludeVolumes: false})
	if err != nil {
		t.Fatalf("Discover without volumes error: %v", err)
	}
	withVolumes, err := Discover(root, Options{IncludeVolumes: true})
	if err != nil {
		t.Fatalf("Discover with volumes error: %v", err)
	}
	if len(withVolumes.Apps) != 1 || len(withoutVolumes.Apps) != 1 {
		t.Fatalf("unexpected app count")
	}
	if withVolumes.Apps[0].EstimatedSizeBytes <= withoutVolumes.Apps[0].EstimatedSizeBytes {
		t.Fatalf("expected volume-inclusive estimate to be larger")
	}
}

func TestBuildAppLabelsDisambiguatesDuplicates(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "stack", "docker-compose.yml")
	second := filepath.Join(root, "stack", "compose.yaml")
	labels := buildAppLabels(root, []string{first, second})
	values := []string{labels[first], labels[second]}
	sort.Strings(values)
	if values[0] == values[1] {
		t.Fatalf("expected unique labels, got %q and %q", values[0], values[1])
	}
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Size()
}
