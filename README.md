# InfraKey (Linux-only MVP)

InfraKey snapshots Docker Compose configuration identity (compose files + referenced env/secrets/config/cert files), encrypts it with [`age`](https://age-encryption.org/), and restores it atomically on a new Linux machine.

## Scope boundary

Included:
- Compose files (`docker-compose.yml/.yaml`, `compose.yml/.yaml`)
- Referenced `env_file` files
- `secrets.*.file` and `configs.*.file` files
- Cert-like paths from compose `environment` values (`.pem`, `.crt`, `.key`, `.p12`) when file exists

Excluded:
- Docker volumes/data blobs/media libraries
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
```

## Commands

```bash
infrakey snapshot --root <dir> --out <vault.bundle> [--recipient <age-pubkey>] [--identity-out <identity.key>]
infrakey restore --bundle <vault.bundle> --identity-key <identity.key> --target <dir> [--yes] [--include-external all|none]
infrakey inspect --bundle <vault.bundle> --identity-key <identity.key>
```

## Snapshot behavior

- Recursively discovers compose files under `--root`
- Skips: `.git`, `node_modules`, `.cache`, `dist`, `build`
- Generates `manifest.pci.json` (`pciVersion: 0.1`)
- Encrypts a deterministic tar payload into `vault.bundle`
- If no recipient is passed, generates an identity key and derives recipient from it

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
