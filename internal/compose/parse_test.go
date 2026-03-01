package compose

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseFile_ExtractsReferences(t *testing.T) {
	tmp := t.TempDir()
	composeDir := filepath.Join(tmp, "stack")
	if err := os.MkdirAll(filepath.Join(composeDir, "certs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(composeDir, "secrets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(composeDir, "certs", "site.crt"), []byte("cert"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(composeDir, "secrets", "db.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	content := `services:
  app:
    env_file:
      - ./.env
      - /etc/global.env
    volumes:
      - ./data:/data
      - /var/lib/app:/srv
      - type: bind
        source: ./bind-data
        target: /bind
    environment:
      TLS_CERT: ./certs/site.crt
secrets:
  db_pass:
    file: ./secrets/db.txt
configs:
  app_cfg:
    file: /etc/app/config.yaml
`
	composePath := filepath.Join(composeDir, "docker-compose.yml")
	if err := os.WriteFile(composePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := ParseFile(composePath)
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	assertMention(t, res.Mentions, MentionEnvFile, "./.env", filepath.Join(composeDir, ".env"))
	assertMention(t, res.Mentions, MentionEnvFile, "/etc/global.env", "/etc/global.env")
	assertMention(t, res.Mentions, MentionVolume, "./data", filepath.Join(composeDir, "data"))
	assertMention(t, res.Mentions, MentionVolume, "/var/lib/app", "/var/lib/app")
	assertMention(t, res.Mentions, MentionVolume, "./bind-data", filepath.Join(composeDir, "bind-data"))
	assertMention(t, res.Mentions, MentionSecret, "./secrets/db.txt", filepath.Join(composeDir, "secrets", "db.txt"))
	assertMention(t, res.Mentions, MentionConfig, "/etc/app/config.yaml", "/etc/app/config.yaml")
	assertMention(t, res.Mentions, MentionCert, "./certs/site.crt", filepath.Join(composeDir, "certs", "site.crt"))
}

func assertMention(t *testing.T, mentions []PathMention, kind, original, resolved string) {
	t.Helper()
	for _, m := range mentions {
		if m.Kind == kind && m.Original == original && filepath.Clean(m.ResolvedAbs) == filepath.Clean(resolved) {
			return
		}
	}
	t.Fatalf("expected mention kind=%s original=%s resolved=%s", kind, original, resolved)
}
