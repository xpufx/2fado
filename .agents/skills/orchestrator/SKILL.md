---
name: orchestrator
description: Workflow, pre-flight audits, agent synchronization, and human-in-the-loop signoff protocols for the orchestrating agent
---

# Orchestrator Skill

You coordinate the fleet. Default: **delegate unless stopped**. Labels describe state; they never gate action.

## 1. Binding stops (only two)

- `priority/0-SOS` — preempt everything, handle first.
- `flag/stop-work` — do not touch, full stop.

Everything else (`spec/*`, `attention/*`, `state/*`, missing labels, one-word tickets) is advisory.

## 2. Delegate by default

- `attention/0-orchestrator`, bare text, or no labels at all still means: infer scope, shape it, dispatch if tree-safe.
- Typical operator input like "build's failing, fix" is sufficient. Pull context yourself (`git status/log`, failing command output, recent comments), form the checklist, set labels yourself, dispatch.
- Only stop-and-ask when: tree-unsafe (operator hands-on in checkout), scope truly uninterpretable, or you need device/credential/2FA input. Ask one question via `attention/2-user`.
- `spec/2-approved` is a hint you've pre-shaped it, not a gate. Never wait for it.
- Slash-commands (`/hold`, `/rework`, `/approve`, etc.): obey when present, never go looking for them. Static labels + ticket text are the primary signal.

## 3. Dispatch

- `list_profiles` first; use profile `notes` to pick model (Free-first, Paid-fallback per notes). Never Gemini for sub-agents unless profile says so.
- One ticket = one worker. Isolate by package dir. Instruct worker: envelope claim comment, `state/1-wip` on start, `state/3-verify` + envelope report on done. Never `git add -A`.
- Tree conflicts gate dispatch: queue, don't collide.

## 4. Pre-flight before human testing (only real gate)

Before `state/3-verify` + `attention/2-user` ("real-use test this"):
- Tree clean, committed, pushed, tests + typecheck green.
- Runtime sync via repo overrides (e.g. `make ready`): PID + hashes + socket healthy. Include provenance card.
- Never present unverified work.

## 5. Verify is non-binding

`state/3-verify` never means "blocked on human forever." If operator doesn't test: close as superseded/done with rationale, requeue, or verify by proxy — and say so on the ticket. No mutual-wait deadlocks.
