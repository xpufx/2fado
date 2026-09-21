# Build and release runbook

This document details how to compile the `2fado` companion binary for host
and cross-platform targets, package distribution archives, verify checksums,
and safely publish release assets.

## Supported platforms

`2fado` is built purely with the Go standard library (`CGO_ENABLED=0`) and
supports the following target architectures:

| Platform | `GOOS` | `GOARCH` | Binary artifact | Archive asset |
|---|---|---|---|---|
| Linux x86_64 | `linux` | `amd64` | `2fado-linux-amd64` | `2fado-<ver>-linux-amd64.tar.gz` |
| Linux ARM64 | `linux` | `arm64` | `2fado-linux-arm64` | `2fado-<ver>-linux-arm64.tar.gz` |
| macOS Intel | `darwin` | `amd64` | `2fado-darwin-amd64` | `2fado-<ver>-darwin-amd64.tar.gz` |
| macOS Apple Silicon | `darwin` | `arm64` | `2fado-darwin-arm64` | `2fado-<ver>-darwin-arm64.tar.gz` |

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

Binaries are emitted to `dist/` (strictly gitignored):

```text
dist/
├── 2fado-darwin-amd64
├── 2fado-darwin-arm64
├── 2fado-linux-amd64
└── 2fado-linux-arm64
```

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
make dist VERSION=0.1.0
```

This performs:
1. Cross-compilation of all 4 platform binaries with embedded version metadata.
2. Bundling each binary into a gzip-compressed tar archive containing `2fado`,
   `LICENSE`, and `README.md`.
3. Generating a unified `SHA256SUMS` file covering all binaries and archives.

Output in `dist/`:

```text
dist/
├── 2fado-0.1.0-darwin-amd64.tar.gz
├── 2fado-0.1.0-darwin-arm64.tar.gz
├── 2fado-0.1.0-linux-amd64.tar.gz
├── 2fado-0.1.0-linux-arm64.tar.gz
├── 2fado-darwin-amd64
├── 2fado-darwin-arm64
├── 2fado-linux-amd64
├── 2fado-linux-arm64
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

Check embedded version, git commit, and sha256 for the host binary:

```sh
./dist/2fado-linux-amd64 version
```

Output should show the expected version and git commit hash:

```text
version: 0.1.0
commit: <short-sha>
build_time: <utc-timestamp>
sha256: <hash>
```

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

## Manual GitHub release workflow

Fleet pushes to public GitHub mirrors are gated by human review. When the
operator prepares a manual release on GitHub:

1. **Tag the release commit** on Forgejo / origin:
   ```sh
   git tag -s v0.1.0 -m "Release v0.1.0"
   ```
2. **Build distribution artifacts**:
   ```sh
   make clean
   make dist VERSION=v0.1.0
   ```
3. **Verify all artifacts**:
   ```sh
   cd dist && sha256sum -c SHA256SUMS
   ```
4. **Draft GitHub Release**:
   - In GitHub repository Releases, create a release draft targeting the tag.
   - Attach all four `.tar.gz` packages and the `SHA256SUMS` file.
   - Optionally attach standalone platform binaries for single-file download.
   - Paste release notes and verification instructions into the release body.
