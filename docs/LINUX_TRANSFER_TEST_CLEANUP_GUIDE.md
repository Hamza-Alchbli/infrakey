# InfraKey Linux Transfer, Test, and Cleanup Guide

This guide covers:
- Building on macOS for Linux
- Copying the binary to a Linux machine
- Snapshot/inspect/restore testing
- Verifying encryption behavior
- Cleaning up all generated artifacts

## 1) Build Linux binary on macOS

Run from the project root:

```bash
cd /Users/hamsa/Documents/projects/infrakey
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/infrakey-linux-amd64 ./cmd/infrakey
ls -lh bin/infrakey-linux-amd64
```

## 2) Transfer binary to Linux machine

```bash
scp /Users/hamsa/Documents/projects/infrakey/bin/infrakey-linux-amd64 <user>@<host>:~/infrakey
```

## 3) Prepare Linux machine

```bash
ssh <user>@<host>
chmod +x ~/infrakey
sudo apt update && sudo apt install -y age
~/infrakey --help
```

## 4) Create snapshot on Linux

```bash
sudo ~/infrakey snapshot --root ~/docker-apps --out ~/vault.bundle --identity-out ~/identity.key
```

Optional full-copy snapshot with chunking:

```bash
sudo ~/infrakey snapshot --root ~/docker-apps --out ~/vault.bundle --identity-out ~/identity.key --full-copy --chunk-size 2GB
```

`--full-copy` captures full compose project directories plus bind-mounted volume sources.

When prompted for snapshot scope:
- choose `All compose apps (...)` to capture everything
- choose `Select compose apps manually` to move with arrow keys, toggle with space, confirm with enter

Optional preflight (no writes):

```bash
sudo ~/infrakey dry-run snapshot --root ~/docker-apps --out ~/vault.bundle --identity-out ~/identity.key
```

## 5) Inspect snapshot metadata

```bash
sudo ~/infrakey inspect --bundle ~/vault.bundle --identity-key ~/identity.key
```

## 6) Restore to clean target directory

```bash
sudo rm -rf /tmp/infrakey-restore-test
sudo ~/infrakey restore --bundle ~/vault.bundle --identity-key ~/identity.key --target /tmp/infrakey-restore-test --yes --include-external none
sudo find /tmp/infrakey-restore-test -maxdepth 4 -type f | sort
```

Optional preflight (no writes):

```bash
sudo ~/infrakey dry-run restore --bundle ~/vault.bundle --identity-key ~/identity.key --target /tmp/infrakey-restore-test --yes --include-external none
```

## 7) Validate restored compose files

```bash
sudo docker compose -f /tmp/infrakey-restore-test/caddy/docker-compose.yml config -q
sudo docker compose -f /tmp/infrakey-restore-test/crafty/docker-compose.yml config -q
sudo docker compose -f /tmp/infrakey-restore-test/immich/docker-compose.yml config -q
sudo docker compose -f /tmp/infrakey-restore-test/nginx-proxy-manager/compose.yaml config -q
sudo docker compose -f /tmp/infrakey-restore-test/vaultwarden/compose.yaml config -q
```

If each command exits silently, compose validation passed.

## 8) Verify encryption/decryption behavior

The bundle should be unreadable without key and decryptable with key.

```bash
# Looks like binary data, not plaintext compose/env
file ~/vault.bundle
strings ~/vault.bundle | head -30

# Decrypt to tar with the correct key (should succeed)
age -d -i ~/identity.key -o /tmp/infrakey-check.tar ~/vault.bundle

# Optional: inspect decrypted tar contents
mkdir -p /tmp/infrakey-check
sudo tar -xf /tmp/infrakey-check.tar -C /tmp/infrakey-check
find /tmp/infrakey-check -maxdepth 3 -type f | sort
```

## 9) Full cleanup (remove all generated artifacts)

```bash
sudo rm -rf /tmp/infrakey-restore-test /tmp/infrakey-check /tmp/infrakey-check.tar
sudo rm -f ~/vault.bundle ~/identity.key ~/vault-2.bundle ~/identity-2.key ~/infrakey
```

Confirm cleanup:

```bash
ls -la ~/vault.bundle ~/identity.key ~/vault-2.bundle ~/identity-2.key ~/infrakey 2>/dev/null || echo "All cleaned"
```

## Notes on what gets captured

InfraKey MVP currently captures:
- Compose files under the snapshot root
- Files referenced by `env_file`
- Files referenced by top-level `secrets.*.file`
- Files referenced by top-level `configs.*.file`
- Cert-like env path values (`.pem`, `.crt`, `.key`, `.p12`) when file exists

InfraKey MVP does not capture:
- Docker images
- Volume data and uploaded media
- Runtime container state
- Arbitrary inline environment variable values unless they are file paths matching cert extensions
