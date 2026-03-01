# Changelog

All notable changes to this project are documented in this file.

Release references in this repository are commit subjects:
- `v1` (`395acbe`, 2026-02-28)
- `1.0.1` (`7c2bfc7`, 2026-03-01)
- `1.1.0` (`165dcf1`, 2026-03-01)

## [1.1.0] - 2026-03-01
Commit: `165dcf1`  
Diff from previous: `git diff --stat 7c2bfc7..165dcf1`  
Summary stats: 17 files changed, 1753 insertions, 147 deletions.

### Added
- Full-copy snapshot mode:
  - `snapshot --full-copy`
  - Captures full compose project directories and bind-mounted volume sources.
- Chunked encrypted output:
  - `snapshot --chunk-size <size>`
  - Default chunking in full-copy mode: `2GB`.
  - Chunk output format introduced as `vault.bundle.parts/part0001...`.
- New chunk handling package and tests:
  - `internal/bundle/chunk.go`
  - `internal/bundle/chunk_test.go`
- Faster inspect sidecar:
  - New encrypted sidecar metadata file support (`inspect.age`) for fast repeated inspect.
  - `internal/bundle/inspect_sidecar.go`
- Progress-capable snapshot/restore internals:
  - Stage-aware events (`staging`, `packaging`, `encrypting`, `chunking`, `validating`, `decrypting`, `extracting`, `restoring`).
  - CLI progress/timer rendering in `cmd/infrakey/main.go`.
- CLI/path normalization and tests:
  - New `cmd/infrakey/main_test.go`.

### Changed
- Snapshot scope estimation now supports volume-aware sizing in full-copy mode (`internal/appselect` updates).
- Manifest model expanded for directory payload support:
  - `entryType` and `dataFormat` (`tar_dir`) support.
- Restore pipeline updated to support both raw-file and directory-tar entries.
- Inspect command updated to use sidecar first when available, with tar fallback.
- CLI improved for chunked bundle input handling.

### Performance
- Restore path refactored to reduce heavy temp-file passes:
  - Streamed chunk reader support.
  - Streamed decrypt/extract path support.
  - Checksum verification shifted into restore copy/extract path.
- Inspect optimized:
  - Sidecar-first manifest path for fast metadata reads.
  - Early-return tar scanning on manifest discovery.
- I/O buffering tuned in tar/copy paths for better throughput.
- Validation flow optimized to avoid unnecessary large temp writes.

### UX / Safety
- Live progress display for snapshot/restore with stage + elapsed + byte metrics.
- Improved status coloring/labels and completion output formatting.
- Better key safety messaging when key/bundle are colocated.
- Permissions adjusted for practical sudo workflows:
  - Restore staging root traversal compatibility.
  - Chunk directory/file readability adjustments.

### Documentation
- README updated with full-copy/chunking behavior and usage.
- Linux transfer/testing guide updated.

### Notes for Upgrades
- `1.1.0` introduces chunked folder-based output (`.parts`) and fast-inspect sidecar behavior.
- Existing non-chunked workflows remain compatible.

---

## [1.0.1] - 2026-03-01
Commit: `7c2bfc7`  
Diff from previous: `git diff --stat 395acbe..7c2bfc7`  
Summary stats: 14 files changed, 1488 insertions, 103 deletions.

### Added
- Interactive snapshot scope selection:
  - “All compose apps” or manual multi-select.
  - Keyboard-driven selector with raw terminal support (`↑/↓`, `Space`, `A`, `Enter`).
  - New prompt implementation and platform rawterm adapters.
- App discovery/estimation package:
  - `internal/appselect` with per-app and total size estimates.
- Dry-run commands:
  - `dry-run snapshot`
  - `dry-run restore`
  - Planning output without writing bundle/restore target.
- Snapshot planning API:
  - `PlanSnapshot`.
- Restore planning API:
  - `PlanRestore`.
- New tests:
  - app selection tests.
  - snapshot planning tests.
  - restore dry-run/no-write tests.

### Changed
- CLI command surface expanded with `dry-run` subcommands.
- Snapshot flow allows explicit compose-path selection via UI selection.
- Build tooling expanded with artifact targets:
  - `make bin`, `make bin-linux`, `make bin-linux-arm64`, `make bin-macos`, `make bin-macos-amd64`, `make bin-all`.
- Key safety warning UX added to snapshot when `--recipient` is omitted.

### UX / Safety
- Improved terminal UX for selection-driven snapshot flow.
- Clearer key-handling warnings to reduce accidental key+bundle co-transfer risk.

### Documentation
- README updated for dry-run and build target usage.
- Linux transfer/test/cleanup guide expanded.

### Notes for Upgrades
- Snapshot became interactive by default for scope choice.
- Scripts that assumed non-interactive `snapshot` need terminal input handling or future flags.

---

## [v1] - 2026-02-28
Commit: `395acbe`  
Initial release baseline.

### Added
- Linux-only MVP CLI with 3 core commands:
  - `snapshot`
  - `restore`
  - `inspect`
- Compose identity capture:
  - Compose files discovery.
  - Capture of referenced `env_file`, `secrets.*.file`, `configs.*.file`.
  - Cert-like env path capture (`.pem`, `.crt`, `.key`, `.p12`) when files exist.
- Manifest v0.1 support (`manifest.pci.json`) with checksums/modes.
- Deterministic tar packaging and `age` encryption.
- Identity key generation path when `--recipient` is not provided.
- Atomic restore model with empty-target enforcement and external reference policy.
- Core packages established:
  - `bundle`, `compose`, `crypto`, `discovery`, `manifest`, `pathmap`, `restore`, `prompt`.
- Baseline unit tests for compose/discovery/manifest/path mapping/restore.
- Initial Makefile and Linux test-transfer docs.

### Scope Boundary at v1
- Included: compose identity files and referenced config/secret/cert/env paths.
- Excluded: docker images, named volumes, runtime state, media/data bulk copy.

---

## Versioning Guidance (For Future Releases)
- Use semantic versions (`MAJOR.MINOR.PATCH`) as commit subject and tag.
- Keep one changelog entry per release with:
  - commit hash
  - date
  - diff range
  - Added / Changed / Fixed / Performance / UX / Breaking notes
- Add explicit “Notes for Upgrades” for behavior changes.
