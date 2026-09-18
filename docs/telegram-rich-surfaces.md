# Telegram rich surfaces for 2fado — research & adoption RFC

- **Issue:** [xpufx-org/2fado#63](https://forge.mrs.uppidi.com/xpufx-org/2fado/issues/63)
- **Status:** Research / RFC only. **No production code changed**; `telegram.go` behaviour is untouched.
- **Scope:** Four Telegram platform surfaces — (1) Mini Apps / Web Apps, (2) Instant View / Telegraph, (3) native expandable blocks + spoilers, (4) paged keyboards + `force_reply`.
- **Grounding:** recommendations are written against the code at commit `19fad44e` (`feat(ask): interactive multi-choice question petitions (MVP)`), which is the closest precedent for interactive surfaces.

---

## 0. Context: how 2fado talks to Telegram today

2fado is **stdlib-only Go**, runs on **private infra with no public hosting**, and pages the operator over Telegram using **outbound long-poll only — no listening socket** (`telegram.Client.Poll` calls `getUpdates`; `service.StartTelegram` starts it). The key seams:

- `internal/telegram/telegram.go` — `Client` (token, `callContext`, `MaxBodyBytes` cap from #60), typed structs (`InlineButton{Text,Data}`, `Keyboard`), `Card`/`NotifyCard`/`AskCard` renderers (all `parse_mode: HTML`, all untrusted fields run through `html.EscapeString`), `Send`/`SendWithMarkup`/`Edit`/`EmptyButtons`, `Poll` + `splitVerdict` (parses `approve:|deny:|ack:|ask:<rid>:<idx>`).
- `internal/service/service.go` — `page`/`closeCard` post/rewrite the approval card; `TelegramPump` is the single consumer of the poll channel and routes `ack`/`ask`/`approve`/`deny` into the store (`Store.Ack`, `Store.Select`, `Store.Consume`).
- `internal/service/ask.go` — the `ask` MVP: `AskWithCancel` → `Store.Save(… Kind:"ask", Options, MultiSelect, AllowWriteIn, RecommendedIndex …)` → `pageAsk` → `awaitSelection` (polls `Store.SelectionInfo` every 200 ms) → `closeAskCard`. The wire schema already persists `MultiSelect`, `AllowWriteIn`, `RecommendedIndex` "from day one so the full ask_question matrix can land later without a data migration."
- `internal/service/authchallenge.go` + `notifyAuthURL`/`AuthButtons` — the 2FA flow: the daemon scans process output for a challenge URL (`ExtractAuthURL`) and surfaces a **URL button** that opens the registry's own WebAuthn/2FA ceremony in the operator's browser. **Credentials never transit Telegram.**

Two constraints shape every recommendation below:

1. **No new authority surface.** Per [#56](https://forge.mrs.uppidi.com/xpufx-org/2fado/issues/56) (root-safety audit), the trust model is agent (untrusted) / operator (approves) / daemon (executes); `isAdminUID` gates verdicts, config, and acks; the **state directory is the authority** (verdicts are `O_EXCL` files in the store); and "Telegram is not an independent root of trust" (F1). Any new surface must write verdicts/selections through the same `Store` primitives and must not add a second authority that a same-uid agent can self-approve.
2. **Fail-closed egress.** Content that leaves the tailnet is a release decision, not a rendering decision. Anything that publishes to a third party must be explicit-opt-in, allowlisted, and redacted **before** send — never automatic.

---

## 1. Mini Apps / Web Apps (in-chat HTML)

**What it is.** A `web_app` button (inline or keyboard) opens an embedded webview running your HTML/CSS/JS. Three data flows matter for 2fado:

- **Keyboard-button Mini App** (`KeyboardButton.web_app`): `Telegram.WebApp.sendData(json)` sends a `web_app_data` string back to the bot as a *service message* — no external server round-trip needed to deliver a small payload.
- **Inline-button Mini App** (`InlineKeyboardButton.web_app`): the webview receives `initData` (user id, name, language, `query_id`, `auth_date`, `hash`) and can call `answerWebAppQuery` to send an inline message on the user's behalf.
- **Auth model.** `initData` is authenticated by computing `secret = HMAC_SHA256("WebAppData", bot_token)` then `hash = HMAC_SHA256(secret, data_check_string)`. This is expressible in stdlib (`crypto/hmac`, `crypto/sha256`, `encoding/json` for the field sort) — **no new Go dependencies**. ([Telegram Mini Apps docs](https://core.telegram.org/bots/webapps), [init-data validation](https://docs.telegram-mini-apps.com/packages/tma-js-init-data-node/validating))

**Feasibility in 2fado's constraints.** Technically yes, but it is the *largest* change of the four areas:

- 2fado has **no HTTP listener today** — the entire Telegram channel is outbound long-poll. A Mini App needs an HTTPS origin the phone can reach. `http://localhost` is unreachable from a phone; the daemon would have to bind a listener **and** get a valid TLS certificate (Telegram Mini Apps require HTTPS). A `tailscale serve`-style `https://<host>.<tailnet>.ts.net` origin is the only realistic "local/tailscale endpoint" that satisfies this, and it assumes the operator's phone is on the tailnet.
- A listener is a **new attack surface**: bind address, TLS config, request auth, and rate/body limits all have to be right, or it recreates the F2-class problems #56 warns about (a second writable authority, a second unauthenticated path). It must not reuse `isAdminUID` and must validate `initData` per-request, not once.
- Serving the UI means a static HTML/CSS/JS bundle (the Telegram Web App JS SDK is a CDN `<script>`), plus a Go handler that renders petition state as JSON. The Go side is stdlib-doable; the *web* side is a new asset we don't currently ship.

**Privacy/security boundary.** No third-party egress *if* the origin stays on the tailnet and the SDK is served from Telegram's CDN — but the operator's browser/phone now talks directly to a 2fado HTTP origin, which is a materially bigger trust surface than "2fado only ever makes outbound `POST`s to `api.telegram.org`." Any bug in `initData` validation = an attacker who can reach the origin can read/approve petitions. The `query_id`/`sendData` payloads must be treated exactly like callback data (untrusted, allowlist-checked against `approvers`).

**Complexity.** Medium-large. Roughly: a new `internal/webapp` package (HTTP server + init-data validation + petition-state JSON), a static bundle, new `InlineButton` fields (`web_app`/`login_url`), `Poll` parsing of `web_app_data` service messages, and tailscale/TLS posture. ~300–600 LOC of Go plus a web bundle, plus ops work for HTTPS.

**Recommendation: defer.** The `ask` MVP + inline keyboards + expandable blocks (areas 3/4) cover today's operator needs at a fraction of the surface. Mini Apps become worth it only when we need *structured multi-field forms* (e.g. per-file diff review with inline approve/deny on individual hunks) that binary buttons can't express. When we do, build it fail-closed: tailscale-only origin, per-request `initData` validation, and **no** direct write path that bypasses `Store`.

---

## 2. Instant View / Telegraph

**What it is.** Telegram's Instant View normally requires Telegram's crawler to fetch a *public* URL — impossible for 2fado's private infra. The escape hatch is the **Telegraph API** ([telegra.ph/api](https://telegra.ph/api)): `createAccount` returns an `access_token` with **zero credentials**, and `createPage` (`content` ≤ 64 KB of a restricted DOM: `p`, `pre`, `code`, `blockquote`, `h3/h4`, `img`, `a`, `figure`, …) returns a public `telegra.ph` URL that **instantly renders in Instant View**.

**Feasibility.** Trivial to call from stdlib Go (`net/http` + `encoding/json`; the Node content format is a JSON array). Effort is not the problem.

**Privacy/security boundary — the crux.** Telegraph is **full third-party egress**, and it is *worse than a normal link* in three ways:

1. **Public and persistent.** A created page is world-readable at a guessable-shape URL. There is **no `deletePage` method**; `editPage` can overwrite/blank the content but the path and URL live on. "Temporary" is not a property of the platform.
2. **New long-lived secret.** The `access_token` becomes a credential 2fado must store, rotate (`revokeAccessToken`), and protect — a new thing to leak, in a tool whose whole point is *not* holding a bigger secret surface.
3. **No redaction guarantees.** The caller must sanitize PII/credentials/hashes *before* `createPage`. A single missing redaction = permanent public disclosure of internal diffs, hostnames, paths, or tokens.

This directly contradicts 2fado's posture (private infra, "no public hosting", "the box record is authority") and #56's authority model — publishing the *authority's* state to a third party is the opposite of fail-closed.

**Fail-closed design (if ever prototyped).** Any Telegraph prototype must: never auto-publish (explicit operator confirmation on a separate `notify`-style petition); require an **allowlist** of destination accounts/topics; run a **redaction pass** (strip secrets, hostnames, absolute paths, uid/username, git remotes) with a dry-run preview the operator approves; and treat the returned URL as a `Link` on a normal notify petition, never as the primary render.

**Complexity.** Low to build, but the operational/security cost is outsized.

**Recommendation: reject for now.** A local Mini App (area 1) is strictly safer for rich docs — data never leaves the tailnet, and Instant View is unavailable anyway for private infra. Keep Telegraph on the table only as an explicit, operator-gated, redacted "publish a sanitized RFC to a teammate who is off the tailnet" one-off, and even then only behind the fail-closed controls above.

---

## 3. Native expandable/collapsible blocks + spoilers

**What it is.** Bot API **7.0** (Dec 29, 2023) added native blockquotes, including `<blockquote expandable>…</blockquote>` (collapsible, one-tap expansion), alongside the older `<tg-spoiler>…</tg-spoiler>` (tap-to-reveal). Both are plain HTML in the same `parse_mode: HTML` messages 2fado already sends. ([Bot API changelog](https://core.telegram.org/bots/api-changelog), [formatting](https://core.telegram.org/bots/api#formatting-options))

**Feasibility.** *This is a pure rendering change inside code that already exists.* `Card`/`NotifyCard`/`AskCard` all emit HTML and already escape untrusted fields. Adding a helper that wraps a (already-escaped) body in `<blockquote expandable>` or `<tg-spoiler>` is ~50–100 LOC plus tests.

**Where it slots in.** The long, low-signal strings that already appear on cards or ride in records are the natural first targets:

- `Preview.RiskReason`, `GitDrift.Reason`, `FDDrift.Reason`, `SealDrift.Reason` (already surfaced on cards/status) → wrap long bodies in `<blockquote expandable>` so a big diff/reason doesn't blow up the card.
- `NotifyCard.Summary` and `AskCard.Question` (already `<pre><code>`) → expandable when over some length, keeping the card compact.
- Execution output/tracebacks (`RunResult.Output`, `StatusResponse.Output`) — a future "result card" or follow-up message could render the tail as an expandable block instead of pasting 256 KiB.
- `SealPin.Files` paths, uids, hostnames → `<tg-spoiler>` where shoulder-surfing is the concern.

**Privacy/security boundary.** **None new.** No new transport, no egress, no new authority. The only existing discipline still applies: escape first, then wrap. Two important caveats to document rather than learn the hard way:

1. `<tg-spoiler>` is a **screen-privacy** control (hides from shoulder-surfers), *not* confidentiality — Telegram still stores the plaintext server-side and it is visible in any client after a tap. It must never be mistaken for redaction (contrast with Telegraph, where redaction actually removes data *before* egress).
2. `<blockquote expandable>` collapses only long-enough quotes; it's cosmetic, not a size cap. The real output bounds remain `MaxOutputBytes` / `MaxTelegramBodyBytes` (#60).

**Complexity.** Small. One render helper + adoption in 2–3 card/status call sites + tests.

**Recommendation: adopt now.** Best value/effort ratio of the four areas, zero security delta, and it lands in seams that already exist. This is the top pick.

---

## 4. Interactive keyboard workflows

Two sub-features, with very different security posture.

### 4a. Paged lists (`[◀️ Prev] [Page 1/3] [Next ▶️]`)

**Feasibility.** The precedent is `AskButtons`, and the plumbing is already there: `splitVerdict` parses structured callback data, `Poll` forwards a `Verdict{Decision, Idx}`, and `TelegramPump` routes it. A `page:<rid>:<n>` (or `page:<rid>:<offset>`) callback kind is a small extension: new `splitVerdict` branch, a `PagedButtons(rid, page, pages)` helper, a store field for a cursor (or, simpler, *no* server cursor — re-`Edit` the message in place with the next page's buttons, embedding the page number in `callback_data`). `callback_data` limit is 64 bytes; `page:` + 16-hex `rid` + page number fits comfortably.

**Privacy/security boundary.** None new — same callback path, same `approvers` allowlist check in `Poll` (`if !approvers[by] { continue }`), same `Store` consumption. The only discipline is to keep pagination *idempotent and re-entrant* (re-tap `Next` = same result, never a double-consume — the store's `O_EXCL` first-wins already gives that for verdicts/selections).

**Complexity.** Small-medium: ~80–150 LOC in `telegram.go` (helper) + `service.go`/`store.go` (paging op) + tests.

**Recommendation: adopt (small).** Highest-value UX for browsing long `Recent`/commit-history outputs; reuses the `ask` seam end to end.

### 4b. `force_reply` for write-in / OTP capture

**What it is.** `reply_markup: {"force_reply": true, "selective": true}` forces the operator into a text reply; the bot receives a normal `message` update (`message.text`) threaded by `reply_to_message_id`.

**Feasibility.** Harder than it looks: `apiUpdate` today only models `callback_query`. Supporting write-in means parsing `message` updates in `getUpdates`, matching `reply_to_message_id` to the originating card, and routing the text into the store — a real extension of `Poll` + `TelegramPump`. The schema groundwork already exists (`AskRequest.AllowWriteIn` is persisted, just not rendered).

**Privacy/security boundary — split verdict.**

- **Write-in responses to `ask`** (finishing `AllowWriteIn`): acceptable, and it would *complete* a field the schema already reserved. The text is operator input like any callback — treat it as untrusted, cap its length (mirror `maxAskOption`), and store/consume it through `Store.Select`. No new egress.
- **OTP / 2FA capture via `force_reply`: reject.** 2fado's existing 2FA design (`ExtractAuthURL` → `notifyAuthURL` → `AuthButtons`) deliberately keeps credentials **out of Telegram** and in the operator's browser with the registry directly. Capturing OTPs in chat would (a) put a third party (Telegram) in the credential path, (b) leave OTPs in persistent chat history, and (c) be strictly worse than the challenge-URL flow for a root-safety tool. Credentials must not transit the pager.

**Complexity.** Medium for write-in (message-update parsing + routing + tests). OTP capture is trivially easy to build and *should still not be built*.

**Recommendation: defer write-in** (nice-to-have that completes `AllowWriteIn`); **reject OTP/credential capture** outright.

---

## Ranked recommendation

| Rank | Surface | Verdict | Effort | Egress / new surface |
| --- | --- | --- | --- | --- |
| 1 | **Native expandable blocks + spoilers** (§3) | **Adopt now** | ~50–100 LOC | none |
| 2 | **Paged inline-keyboard lists** (§4a) | **Adopt (small)** | ~80–150 LOC | none |
| 3 | **Mini Apps / Web Apps** (§1) | **Defer** | ~300–600 LOC + bundle + HTTPS/tailnet | new local HTTP origin (no third-party egress if tailnet-only) |
| 4 | **`force_reply` write-in** (§4b) | **Defer** | ~150–250 LOC | none (completes `AllowWriteIn`) |
| 5 | **`force_reply` OTP capture** (§4b) | **Reject** | n/a | puts credentials in Telegram chat — regression vs. `AuthButtons` |
| 6 | **Telegraph publishing** (§2) | **Reject now** | low build, high ops | **third-party egress**, public + persistent |

**Bottom line.** Adopt **1 + 2** — both are small, stdlib-only, and bolt onto the `ask`/card seams that `19fad44e` already established, with zero new egress or authority. Defer **3** until structured multi-field diff review is a real requirement, and when it arrives, make it tailnet-only with per-request `initData` validation. Reject **5** and **6** on the security boundary: 2fado's trust model (#56) and its "no public hosting / credentials stay with the registry" posture both argue against pushing internal content or credentials through a third party.

---

## Appendix — RFC sketch (illustrative only, NOT production)

Marked sketch: these snippets are design notes, not committed behaviour, and none of them are wired in.

```go
// telegram.go (sketch): helpers over existing escape-first rendering.
// Wrap an ALREADY-escaped body; never escape inside these.
func Expandable(body string) string  { return "<blockquote expandable>" + body + "</blockquote>" }
func Spoiler(body string) string     { return "<tg-spoiler>" + body + "</tg-spoiler>" }

// PagedButtons (sketch): data "page:<rid>:<n>"; n is 0-based, re-Edit in place.
func PagedButtons(rid string, page, pages int) string {
    nav := []InlineButton{}
    if page > 0        { nav = append(nav, InlineButton{Text: "◀️ Prev", Data: "page:" + rid + ":" + strconv.Itoa(page-1)}) }
    nav = append(nav, InlineButton{Text: fmt.Sprintf("Page %d/%d", page+1, pages), Data: "noop"})
    if page+1 < pages  { nav = append(nav, InlineButton{Text: "Next ▶️", Data: "page:" + rid + ":" + strconv.Itoa(page+1)}) }
    kb, _ := json.Marshal(Keyboard{Buttons: [][]InlineButton{nav}})
    return string(kb)
}

// splitVerdict (sketch): add one branch.
if strings.HasPrefix(data, "page:") {
    rest := strings.TrimPrefix(data, "page:")
    i := strings.LastIndex(rest, ":")
    if i <= 0 { return "", "", 0, false }
    n, err := strconv.Atoi(rest[i+1:])
    if err != nil || n < 0 { return "", "", 0, false }
    return "page", rest[:i], n, true
}
```

If either sketched feature is picked up, it should land as its own ticket following the `ask` MVP's shape: schema fields reserved first (so no data migration), render second, transport third — and every new verdict/selection path must go through the existing `Store` `O_EXCL` primitives rather than a new write path.
