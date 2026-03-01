# InfraKey (Linux-only MVP)

InfraKey snapshots Docker Compose configuration identity (compose files + referenced env/secrets/config/cert files), encrypts it with [`age`](https://age-encryption.org/), and restores it atomically on a new Linux machine.

## Scope boundary

Included:
- Compose files (`docker-compose.yml/.yaml`, `compose.yml/.yaml`)
- Referenced `env_file` files
- `secrets.*.file` and `configs.*.file` files
- Cert-like paths from compose `environment` values (`.pem`, `.crt`, `.key`, `.p12`) when file exists
- Optional full-copy capture of full compose project directories and bind-mounted volume sources (`--full-copy`)

Excluded:
- Docker named volumes (not yet captured in this mode)
- Docker images
- Runtime container/network state

## Requirements

- Linux runtime (MVP restriction)
- `age` binary in `PATH`
- `age-keygen` binary in `PATH` (only required when `--recipient` is not provided)

## Build and test

```bash
make build
make test
make bin-linux
```

`make bin-linux` creates `bin/infrakey-linux-amd64` for copying to your Linux host.
Future-ready targets are also available: `make bin`, `make bin-linux-arm64`, `make bin-macos`, `make bin-macos-amd64`, `make bin-all`.

## Commands

```bash
infrakey snapshot --root <dir> --out <vault.bundle> [--recipient <age-pubkey>] [--identity-out <identity.key>] [--full-copy] [--chunk-size <size>]
infrakey restore --bundle <vault.bundle> --identity-key <identity.key> --target <dir> [--yes] [--include-external all|none]
infrakey inspect --bundle <vault.bundle> --identity-key <identity.key>
infrakey dry-run snapshot --root <dir> --out <vault.bundle> [--recipient <age-pubkey>] [--identity-out <identity.key>] [--full-copy] [--chunk-size <size>]
infrakey dry-run restore --bundle <vault.bundle> --identity-key <identity.key> --target <dir> [--yes] [--include-external all|none]
```

Extended snapshot flags:
- `--full-copy` include full compose project directories plus bind-mounted compose volume sources (files/directories)
- `--chunk-size <size>` split encrypted output into chunk files (example: `2GB`)

`dry-run` commands execute discovery/validation/decryption and print planned actions, but do not write output bundles, keys, or restore targets.

## Snapshot behavior

- Recursively discovers compose files under `--root`
- Skips: `.git`, `node_modules`, `.cache`, `dist`, `build`
- Always prompts snapshot scope:
  - `All compose apps` (with total estimated capture size)
  - `Select compose apps manually` (checkbox list with per-app estimated size)
- If snapshot is generating a key (no `--recipient`), CLI prints a security notice and warns when key and bundle are in the same directory.
- Generates `manifest.pci.json` (`pciVersion: 0.1`)
- Encrypts a deterministic tar payload into `vault.bundle`
- In full-copy mode, default chunking is `2GB` (example outputs: `vault.bundle.parts/part0001`, `vault.bundle.parts/part0002`)
- If no recipient is passed, generates an identity key and derives recipient from it
- For chunked output, keep using the base bundle path (`--bundle ~/vault.bundle`) for `inspect` and `restore`.

## Restore behavior

- Enforces target directory to be empty or absent
- Decrypts into temp workspace, validates checksums, then commits atomically
- Supports explicit external reference policy:
  - Interactive prompts by default
  - Non-interactive mode requires `--yes --include-external all|none`
- Rolls back staging files on failure

## PCI manifest (v0.1)

`manifest.pci.json` contains:
- `pciVersion`, `snapshotId`, `createdAt`, `sourceRoot`
- `entries[]`: `id`, `kind`, `sourceAbsPath`, `sourceRelPath`, `restoreRelPath`, `sha256`, `mode`
- `composeRewrites[]`: `composeEntryId`, `replacements[]`
- `outsideRootEntries[]`
