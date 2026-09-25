# Build and release runbook

This document details how to compile the `2fado` companion binary for host
and cross-platform targets, emit versioned bare release assets (plus optional
legacy tar archives), verify checksums, and publish release assets through the
automated Forgejo → GitHub mirror pipeline.

## Supported platforms

`2fado` is built purely with the Go standard library (`CGO_ENABLED=0`) and
ships these target architectures by default:

| Platform | `GOOS` | `GOARCH` | Bare binary asset | Legacy archive asset |
|---|---|---|---|---|
| Linux x86_64 | `linux` | `amd64` | `2fado-<ver>-linux-amd64` | `2fado-<ver>-linux-amd64.tar.gz` |
| Linux ARM64 | `linux` | `arm64` | `2fado-<ver>-linux-arm64` | `2fado-<ver>-linux-arm64.tar.gz` |
| macOS Intel | `darwin` | `amd64` | `2fado-<ver>-darwin-amd64` | `2fado-<ver>-darwin-amd64.tar.gz` |
| macOS Apple Silicon | `darwin` | `arm64` | `2fado-<ver>-darwin-arm64` | `2fado-<ver>-darwin-arm64.tar.gz` |

Asset names embed the build version — that is the asset name
`scripts/install-companion.mjs` and the release mirror look for.

**`windows/amd64` is not in the default set and does not compile today.**
`scripts/build-cross.sh` can name the target and `install-companion.mjs` maps
`win32-x64` onto `windows/amd64`, but `go build` fails on the unix-only
`internal/store/secure_unix.go` helpers (`noFollow`, `writeNoFollow`,
`appendNoFollow` are undefined for GOOS=windows). Building it needs Windows
stubs in `internal/config/owner_unix.go` and `internal/store/secure_unix.go`
first; treat the installer's Windows mapping as forward-looking.

## Quick build

### Host native build

To build `2fado` for your current host:

```sh
make build
./bin/2fado version
```

The output binary is written to `bin/2fado` (gitignored).

### Cross-compilation for all platforms

To build standalone companion binaries for all four supported platforms:

```sh
make cross-build
```

Or invoke the helper script directly:

```sh
./scripts/build-cross.sh
```

`scripts/build-cross.sh` accepts `--package`, `--clean`, `--dist-dir <dir>`,
`--version <ver>`, plus the environment overrides `VERSION` and `DIST_DIR`.
Target selection uses the in-script bash array `PLATFORMS`, whose
`DEFAULT_PLATFORMS` is the four-target set above — an environment override of
`PLATFORMS` is therefore only honoured for a **single** `<os>/<arch>` pair
(verified: `PLATFORMS="darwin/arm64" ./scripts/build-cross.sh` builds exactly
that one target; a space-separated list is passed through as one malformed
pair and fails with `unsupported GOOS/GOARCH pair`).

Binaries are emitted to `dist/` (strictly gitignored), each named
`2fado-<version>-<os>-<arch>` (`.exe` appended for Windows):

```text
dist/
├── 2fado-0.1.3-darwin-amd64
├── 2fado-0.1.3-darwin-arm64
├── 2fado-0.1.3-linux-amd64
├── 2fado-0.1.3-linux-arm64
└── SHA256SUMS
```

`SHA256SUMS` covers the bare binaries only; `--package`/`make dist` regenerates
it to also cover the tar archives. The host-native `make build` output
(`bin/2fado`) is a *separate*, unversioned artifact and is not part of `dist/`.

### Building for a specific non-Linux target

If working on or building specifically for macOS (e.g. Apple Silicon `darwin/arm64`):

```sh
# Via Go CLI directly
cd go && CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o ../bin/2fado ./cmd/2fado

# Or via the helper script
PLATFORMS="darwin/arm64" ./scripts/build-cross.sh
```

## Distribution packaging (`make dist`)

For tagged releases or external distribution, generate tarball archives and
checksum manifests:

```sh
make dist VERSION=0.1.3
```

This performs:
1. Cross-compilation of all 4 platform binaries with embedded version metadata.
2. Bundling each binary into a gzip-compressed tar archive containing `2fado`,
   `LICENSE`, and `README.md` (legacy asset shape, kept for older installers).
3. Generating a unified `SHA256SUMS` file covering all binaries and archives.

Output in `dist/`:

```text
dist/
├── 2fado-0.1.3-darwin-amd64.tar.gz
├── 2fado-0.1.3-darwin-arm64.tar.gz
├── 2fado-0.1.3-linux-amd64.tar.gz
├── 2fado-0.1.3-linux-arm64.tar.gz
├── 2fado-0.1.3-darwin-amd64
├── 2fado-0.1.3-darwin-arm64
├── 2fado-0.1.3-linux-amd64
├── 2fado-0.1.3-linux-arm64
└── SHA256SUMS
```

### Cleaning artifacts

To clean all build outputs and temporary directories:

```sh
make clean
```

Removes `bin/2fado`, `dist/`, and isolated dev run directories (`.testrun/`).

## Verification runbook

### 1. Verify checksums

Before publishing, verify all artifact checksums match:

```sh
cd dist && sha256sum -c SHA256SUMS
```

### 2. Verify binary metadata

Check embedded version, git commit, and sha256 for a host binary:

```sh
./dist/2fado-0.1.3-linux-amd64 version
```

Output should show the expected version and git commit hash:

```text
version: 0.1.3
commit: <short-sha>
build_time: <utc-timestamp>
sha256: <hash>
```

The bare asset's own sha256 must also be listed in `dist/SHA256SUMS`; that is
the same digest the plugin installer verifies before writing `bin/2fado`.

### 3. Verify escalation exclusion (#61)

The default build strictly excludes privilege escalation code (`internal/service/escalation_on.go`):

```sh
make verify-escalation-excluded
```

This ensures symbols and logic for credential switching are provably absent
from public release binaries.

### 4. Verify git clean state

Verify no binary artifacts or archives are tracked or untracked in git:

```sh
git status
```

`dist/`, `bin/`, and `*.tar.gz` are strictly excluded by `.gitignore`.

## Release workflow (automated)

Releases are no longer a hand-assembled GitHub step. `.forgejo/workflows/` is
the canonical CI/CD source; `.github/workflows/` holds a manual-dispatch-only
copy (see `.github/workflows/README.md` for the split and the placement
constraint that forces the reusable workflow to sit in the `.github/workflows`
root).

### Forgejo (canonical)

`.forgejo/workflows/cross-build.yml` fires on **release published**, on **push
of a `v*` tag**, or on **`workflow_dispatch`** with an optional `version`
input. It delegates to `.forgejo/workflows/reusable/cross-build.yml` on
`ubuntu-24.04`, which:

1. Derives the build version from the input, else the tag with any leading `v`
   stripped, else `0.1.0-dev`.
2. Runs `make clean`, `make test`, `make dist VERSION=<ver>`.
3. Runs `sha256sum -c SHA256SUMS` and asserts each of the four versioned bare
   binaries is present and non-empty.
4. Uploads a `2fado-cross-dist-<ver>` workflow artifact and writes a step
   summary on every run.
5. Only when the trigger was `release: published` (the caller sets
   `upload_release_assets: ${{ github.event_name == 'release' }}`) does it
   attach every `dist/2fado-*` asset to that Forgejo release via the API and
   mirror the same assets to the public GitHub mirror with
   `scripts/publish-github-release.sh --tag v<ver>` (via `gh` when
   authenticated, REST API fallback otherwise).

So the publishing path is: create the release (or publish a tag-built
release) → CI attaches the assets to the Forgejo release and mirrors them to
GitHub. A bare `v*` tag push builds, tests and verifies, but attaches nothing
unless a release is published. Pushes of source to the public GitHub mirror
remain human-gated; only release-asset mirroring is automated.

### Reproducing a release locally

```sh
git tag -s v0.1.3 -m "Release v0.1.3"
git push origin v0.1.3
make clean && make test && make dist VERSION=0.1.3
(cd dist && sha256sum -c SHA256SUMS)
```

Note that `make dist VERSION=v0.1.3` bakes the leading `v` into the asset
names (`2fado-v0.1.3-…`) and will not match the `v`-stripped names published
by CI. Pass the bare version (`VERSION=0.1.3`) when you want local output to
match the release assets.

### GitHub Actions copy

`.github/workflows/cross-build.yml` is the same pipeline with the tag and
release triggers removed — it runs on **`workflow_dispatch` only** (inputs
`version`, `upload_release_assets`, default `false`) to avoid double
execution. It passes `release_id: 0`, so it never touches a Forgejo release,
and with `upload_release_assets: true` it mirrors the assets to GitHub
Releases.
