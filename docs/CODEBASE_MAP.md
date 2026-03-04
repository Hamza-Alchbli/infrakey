# InfraKey Codebase Map (Plain English)

This project is a CLI that snapshots Docker Compose project identity (compose files + referenced files), encrypts it, and restores it later.

If you only remember one thing: `cmd/infrakey/main.go` is CLI wiring; real behavior lives in `internal/*`.

## High-level flow

`snapshot` flow:
1. CLI parses flags in `cmd/infrakey/main.go`.
2. App discovery/selection is done in `internal/appselect`.
3. Snapshot plan + manifest is built in `internal/bundle/snapshot.go`.
4. Payload tar creation/extraction helpers are in `internal/bundle/tar.go`.
5. Encryption/decryption shell-outs to `age` in `internal/crypto/age.go`.

`restore` flow:
1. CLI parses flags in `cmd/infrakey/main.go`.
2. Bundle is opened from single file or chunk parts in `internal/bundle/chunk.go`.
3. Payload is decrypted and extracted in `internal/restore/restore.go`.
4. Manifest entries are restored, checksummed, and committed atomically in `internal/restore/restore.go`.
5. Compose absolute paths can be rewritten to new target paths in `applyComposeRewrites`.

`inspect` flow:
1. CLI parses flags in `cmd/infrakey/main.go`.
2. Inspect reads encrypted sidecar if present (`internal/bundle/inspect_sidecar.go`) or decrypts bundle and reads manifest (`internal/bundle/inspect.go`).

## Package map

- `cmd/infrakey`: command parsing, progress output, usage text.
- `internal/discovery`: recursively finds compose files.
- `internal/compose`: custom line-based parser for compose references (env_file, volumes, secrets/configs, cert-like env values).
- `internal/appselect`: calculates per-app estimated capture size and presents selection list.
- `internal/bundle`: snapshot planning, staging, tar/chunk helpers, inspect support.
- `internal/restore`: target validation, decryption, checksum verification, atomic commit.
- `internal/manifest`: PCI manifest schema + validation.
- `internal/pathmap`: safe path mapping; blocks restore path escapes.
- `internal/crypto`: wrappers around `age`/`age-keygen` command execution.
- `internal/prompt`: interactive terminal prompts and selection UI.

## Current confidence vs risk

What is currently strong:
- Unit tests exist for discovery, compose parsing basics, app selection, chunk splitting, and some path safety.
- `go test ./...`, `go test -race ./...`, and `go vet ./...` pass.
- Path traversal protections exist in tar extraction + target path mapping.

What is currently weaker:
- End-to-end snapshot/restore pipelines are lightly tested.
- `internal/crypto` has no direct tests.
- `internal/prompt` has no tests.
- Overall statement coverage is low for critical runtime paths (`internal/bundle` and `internal/restore`).

## One-command health check

Run this anytime:

```bash
make audit
```

It runs:
1. `go test ./...`
2. `go test -race ./...`
3. `go vet ./...`

## How to work safely without knowing Go

Use this playbook before and after any change:
1. Run `make audit`.
2. Run `infrakey dry-run snapshot ...` on a representative compose project.
3. Run `infrakey dry-run restore ...` on the produced bundle.
4. Compare planned entries with expected files, especially external references.

If something looks wrong, start debugging from:
1. `internal/compose/parse.go` (reference detection)
2. `internal/bundle/snapshot.go` (what gets included in manifest)
3. `internal/restore/restore.go` (where/what gets restored)
