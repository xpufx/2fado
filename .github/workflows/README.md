# Multi-Forge Actions Workflows

The workflow definitions in this directory (`.github/workflows/`) serve as the canonical CI/CD workflows compatible across both **Forgejo Actions** and **GitHub Actions**.

Forgejo Actions natively discovers and executes workflows placed in `.github/workflows/`. Consolidating shared workflows here eliminates duplicate definitions and synchronization drift across forges.

## Directory Standards

1. **Multi-Forge Portable Workflows (`.github/workflows/`)**:
   - Workflows that run in standard container environments (`runs-on: ubuntu-24.04`) across both Forgejo and GitHub Actions.
   - Example: `cross-build.yml` and `reusable-cross-build.yml`.
   - **GitHub Actions Placement Constraint**: GitHub Actions requires all workflow definitions (including reusable workflows) to reside directly at the root of `.github/workflows/` (nested directories like `reusable/` cause HTTP 422 errors). Reusable workflows are therefore named with flat prefixes such as `reusable-cross-build.yml`.

2. **Forgejo-Specific Host Workflows (`.forgejo/workflows/`)**:
   - Workflows that execute exclusively on Forgejo host runners (`runs-on: runner-local-shell`) or interact directly with local host sockets/scripts (such as `issue-label-triage.yml`).
   - Kept under `.forgejo/workflows/` so GitHub Actions mirrors do not attempt to execute host-specific workflows lacking matching runners or dependencies.
