# Agent Workflow & Task Board Protocol

This repository uses **Forgejo** (`forge.mrs.uppidi.com/xpufx-org/2fado`) as its issue tracking board and primary git remote.

## 1. Issue Tracking with `fgjx`
All task pickup, status changes, and handoffs must go through `fgjx`:
```bash
# Check issues on this repository
fgjx --hostname forge.mrs.uppidi.com -R xpufx-org/2fado issue list

# View an issue and full thread
fgjx --hostname forge.mrs.uppidi.com -R xpufx-org/2fado issue view <ID>
```

## 2. Coding Skills Installed
The standardized coding and plugin skills are available in `.agents/skills/`:
- `.agents/skills/coding-agent/SKILL.md`: Task lifecycle, envelope self-stamping, worktree isolation, and commit tracking.
- `.agents/skills/audit-plugin/SKILL.md`: Migration guide to `paseo-plugin-helper`.
- `.agents/skills/create-plugin/SKILL.md`: Plugin structure, contracts, and v8 layout.

## 3. Mandatory Plugin Rule: Helper First
All plugin code in `plugin/` MUST build upon `paseo-plugin-helper@beta`.
- Use `initClientHelpers` and `registerComposerPill` in client.
- Wrap daemon socket calls with `guardRpcHandler` in server.
- Use `defineContract` in shared.
- Never write bespoke raw SDK boilerplate where helper primitives exist.
