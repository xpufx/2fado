# GitHub Actions Workflows (Temporary Setup)

The workflow definitions in this directory (`.github/workflows/`) are copied from `.forgejo/workflows/` to allow on-demand cross-platform builds directly on GitHub Actions via `workflow_dispatch`.

> [!NOTE]
> This directory is maintained temporarily until CI/CD workflows across `.forgejo` and `.github` are standardized organization-wide.
>
> **Canonical Source**: The primary, authoritative CI/CD and release automation continues to reside in [`.forgejo/workflows/`](../../.forgejo/workflows/).
>
> **Execution Policy**:
> - Automatic triggers on tags (`v*`) and release publication are handled exclusively by Forgejo CI to avoid double execution.
> - The workflows here are restricted to manual execution (`workflow_dispatch`) on GitHub Actions.
> - Automated build artifacts from Forgejo releases are continuously mirrored to GitHub Releases via `scripts/publish-github-release.sh`.
>
> **GitHub Actions Constraints**:
> - GitHub Actions requires all workflow files, including reusable workflows, to be placed directly in the root of `.github/workflows/` (subdirectories like `reusable/` are rejected with HTTP 422). Therefore, the reusable workflow is stored as `reusable-cross-build.yml`.
