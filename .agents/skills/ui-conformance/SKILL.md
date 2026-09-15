---
name: ui-conformance
description: Audit and migrate 2fado plugin UI toward paseo-plugin-helper primitives
---

# 2fado UI Conformance

Use this skill before changing user-facing UI in `plugin/client`.

## Source of truth

The 2fado plugin already depends on the same `paseo-plugin-helper` library
used by the Paseo monorepo. Use its exported client primitives directly; do
not create a local substitute or a second component vocabulary.

The conformance scanner source of truth lives in the Paseo monorepo:

```text
~/code/paseo/packages/paseo-plugin-helper/bin/paseo-plugin-helper.js
```

Do not copy the scanner into 2fado. Run the shared scanner from the sibling
Paseo checkout so fixes are evaluated against the same rules as the other
plugins. The scanner and the runtime helper library are related but separate:
the scanner checks source usage, while `paseo-plugin-helper/client` supplies
the actual UI components.

## Commands

From the 2fado repository:

```bash
# Full helper audit of the plugin
~/code/paseo/packages/paseo-plugin-helper/bin/paseo-plugin-helper.js \
  doctor plugin --format json

# UI/helper conformance for this plugin
~/code/paseo/packages/paseo-plugin-helper/bin/paseo-plugin-helper.js \
  conformance plugin --strict

# Typecheck the plugin
npm --prefix plugin run typecheck
```

If the Paseo checkout is elsewhere, set `PASEO_REPO` and use:

```bash
"${PASEO_REPO}/packages/paseo-plugin-helper/bin/paseo-plugin-helper.js" \
  conformance plugin --strict
```

## Migration rules

- Prefer the installed `paseo-plugin-helper/client` components for buttons, tabs, toggles,
  inputs, cards, form rows, status indicators, empty states, key/value groups,
  modal bodies, and action bars.
- Use `InlineButton` for compact links and inline actions rather than creating
  a local `Pressable` link.
- Do not use raw `Pressable` for ordinary actions when `Button`, `Tabs`,
  `Collapsible`, or another helper interaction primitive fits.
- Do not introduce a second local design system with `StyleSheet`, custom
  borders, hand-built cards, or fixed modal dimensions.
- Keep raw `View` and `Text` only for ordinary composition or content that has
  no suitable helper equivalent.
- Preserve behavior, accessibility, loading/error states, and compact/mobile
  scrolling.
- Do not weaken the scanner, add plugin-specific allowlists, or suppress a
  finding merely to make the command pass.

## Helper-gap protocol

If the installed helper does not support the required interaction, do not
silently hand-roll a replacement:

1. Record the exact behavior and the helper APIs considered.
2. Decide whether the gap is reusable by other plugins.
3. For a reusable gap, propose or implement the additive helper API in the
   shared Paseo helper repository and add tests/docs without changing existing
   contracts.
4. For a genuinely 2fado-specific behavior, retain the smallest local
   composition and document why it is not reusable.

Never add a scanner allowlist to hide an unresolved helper gap. Link the
shared helper issue or commit from the 2fado issue.

## Completion gate

Before handing off UI work:

1. Run the strict conformance command.
2. Run the plugin typecheck.
3. Run relevant tests.
4. Include the scanner output and any intentionally retained raw React Native
   usage in the Forgejo issue completion comment.

The issue is not UI-conformant while strict conformance reports findings.
