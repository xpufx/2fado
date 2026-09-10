# Backburner: approval-card spoofing + raw sidecar

## Problem
`telegram.Card()` splices `strings.Join(argv, " ")` raw into a Markdown
message with triple-backtick fences (`go/internal/telegram/telegram.go`).
Attacker-controlled argv can break the fence (`\n```\n`) and inject
fake `✅ Approved`-looking blocks, hide args with `\r`/ANSI, or blur
argv boundaries (`["sh" "-c" "x"]` vs `["sh -c x"]` render the same).
The daemon executes the stored argv, but the human approves what they see.

## Idea: raw sidecar
Send a second message with no `parse_mode` containing the exact argv
(boundary-explicit, e.g. `%q` one-per-line). Without `parse_mode`
Telegram renders it literally, so fence/bold/link injection is inert.

## What it fixes / doesn't
- Fixes: fence-break, bold/italic/link spoofing.
- Doesn't fix: zero-width chars, bidi (`U+202E`), homoglyphs, `\r`/ANSI —
  those fool humans in plain text too. Sidecar still needs control-char
  escaping and explicit boundaries, not raw `Join`.

## Cheaper alternative
Single message with no `parse_mode`, or properly escaped MarkdownV2/HTML.
Sidecar costs a second `MsgID` (store/protocol change), double-buzz UX,
and two edits on `closeCard`.

## Open
- Decide single escaped message vs sidecar.
- Define argv rendering (`%q`, control-char/bidi escaping).
- Track second `MsgID` if sidecar wins.
