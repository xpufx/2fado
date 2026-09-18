// Package telegram speaks Bot API with typed structs only. It renders
// the approval card and runs the outbound long-poll verdict loop.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os/user"
	"strconv"
	"strings"
	"time"
)

type InlineButton struct {
	Text string `json:"text"`
	Data string `json:"callback_data"`
}

type Keyboard struct {
	Buttons [][]InlineButton `json:"inline_keyboard"`
}

type apiMessage struct {
	ID   int64 `json:"message_id"`
	Chat struct {
		ID int64 `json:"id"`
	} `json:"chat"`
}

type apiResponse struct {
	OK     bool       `json:"ok"`
	Result apiMessage `json:"result"`
}

type apiUser struct {
	ID int64 `json:"id"`
}

type apiCallback struct {
	ID   string  `json:"id"`
	From apiUser `json:"from"`
	Data string  `json:"data"`
}

type apiUpdate struct {
	ID       int64       `json:"update_id"`
	Callback apiCallback `json:"callback_query"`
}

type apiUpdates struct {
	OK     bool        `json:"ok"`
	Result []apiUpdate `json:"result"`
}

type Client struct {
	Token   string
	BaseURL string
	HTTP    *http.Client
}

func New(token string) Client {
	return Client{Token: token, HTTP: &http.Client{Timeout: 40 * time.Second}}
}

// MaxBodyBytes bounds a single Bot API response body read (#60).
// Overlong bodies are cut; callers see a JSON error instead of OOM.
var MaxBodyBytes = int64(1 << 20)

func (c Client) baseURL() string {
	if c.BaseURL != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	return "https://api.telegram.org"
}

func (c Client) call(method string, v url.Values) ([]byte, error) {
	return c.callContext(context.Background(), method, v)
}

func (c Client) callContext(ctx context.Context, method string, v url.Values) ([]byte, error) {
	httpCli := c.HTTP
	if httpCli == nil {
		httpCli = &http.Client{Timeout: 40 * time.Second}
	}
	urlStr := fmt.Sprintf("%s/bot%s/%s", c.baseURL(), c.Token, method)
	req, err := http.NewRequestWithContext(ctx, "POST", urlStr, strings.NewReader(v.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := httpCli.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var b bytes.Buffer
	if _, err := io.Copy(&b, io.LimitReader(resp.Body, MaxBodyBytes)); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

type apiUserResult struct {
	OK          bool   `json:"ok"`
	Description string `json:"description,omitempty"`
	Result      struct {
		ID        int64  `json:"id"`
		IsBot     bool   `json:"is_bot"`
		FirstName string `json:"first_name"`
		Username  string `json:"username"`
	} `json:"result"`
}

// GetMe queries Telegram getMe to validate the bot token and retrieve bot username.
func (c Client) GetMe() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	return c.GetMeContext(ctx)
}

// GetMeContext queries getMe with a caller-provided context.
func (c Client) GetMeContext(ctx context.Context) (string, error) {
	if c.Token == "" || strings.HasPrefix(c.Token, "__") {
		return "", fmt.Errorf("token is unconfigured")
	}
	body, err := c.callContext(ctx, "getMe", url.Values{})
	if err != nil {
		return "", err
	}
	var res apiUserResult
	if err := json.Unmarshal(body, &res); err != nil {
		return "", fmt.Errorf("invalid response: %w", err)
	}
	if !res.OK {
		if res.Description != "" {
			return "", fmt.Errorf("telegram api error: %s", res.Description)
		}
		return "", fmt.Errorf("telegram api: !ok")
	}
	return res.Result.Username, nil
}

func Who(uid uint32) string {
	if u, err := user.LookupId(strconv.FormatUint(uint64(uid), 10)); err == nil {
		return fmt.Sprintf("%s (uid %d)", u.Username, uid)
	}
	return fmt.Sprintf("uid %d", uid)
}

// maxCommandRunes bounds the rendered command so overlong arguments
// cannot push the request id or buttons out of view.
const maxCommandRunes = 1500

// ExpandableMinRunes is the visible-text length above which a long body
// is collapsed behind <blockquote expandable> so cards stay compact by
// default with one-tap expansion. Bot API 7.0+ renders it; older clients
// degrade to a plain blockquote.
const ExpandableMinRunes = 280

// Expandable wraps an ALREADY-ESCAPED HTML body in a collapsible
// blockquote. The caller owns escaping: this helper only adds markup and
// must never be handed raw user/command text. Empty bodies pass through
// so no empty quote is emitted.
func Expandable(body string) string {
	if body == "" {
		return ""
	}
	return "<blockquote expandable>" + body + "</blockquote>"
}

// Spoiler wraps an ALREADY-ESCAPED HTML body in a tap-to-reveal spoiler.
// It is screen privacy only — Telegram still stores the plaintext and any
// client shows it after a tap — so it must never be mistaken for
// redaction. The caller owns escaping.
func Spoiler(body string) string {
	if body == "" {
		return ""
	}
	return "<tg-spoiler>" + body + "</tg-spoiler>"
}

// expandablePre renders already-escaped text as a code block, collapsing
// it behind an expandable quote when the text is long enough to matter.
func expandablePre(escaped string) string {
	block := "<pre><code>" + escaped + "</code></pre>"
	if len([]rune(escaped)) <= ExpandableMinRunes {
		return block
	}
	return Expandable(block)
}

// spillID renders an already-escaped request id as a spoiler-wrapped code
// token: the id is an opaque capability-shaped handle that need not be
// readable to a shoulder-surfer.
func spillID(rid string) string {
	return Spoiler("<code>" + rid + "</code>")
}

// Card renders the approval request as HTML (parse_mode HTML). All
// untrusted fields are html-escaped so embedded backticks, tags, or
// entities cannot break out of the code block or spoof card layout.
// Verdict is "" while pending, "confirm" for the second-step challenge,
// otherwise the resolution ("approve", "deny", "timeout", ...); by names
// the resolution source and is shown on closed cards.
func Card(host string, argv []string, uid uint32, cwd, rid string, expiresIn int64, verdict string, by string, dry bool, asUID uint32) string {
	head := resolutionHead(verdict, by, dry)
	cmd := strings.Join(argv, " ")
	if r := []rune(cmd); len(r) > maxCommandRunes {
		cmd = string(r[:maxCommandRunes]) + "…[truncated]"
	}
	return fmt.Sprintf("%s\n🖥️ host: <code>%s</code>\n👤 caller: <code>%s</code>\n🔑 will run as: <code>%s</code>\n📁 cwd: <code>%s</code>\n⌨️ command:\n%s\n🕒 expires in %ds · request %s",
		head, html.EscapeString(host), html.EscapeString(Who(uid)), html.EscapeString(Who(asUID)), html.EscapeString(cwd), expandablePre(html.EscapeString(cmd)), expiresIn, spillID(html.EscapeString(rid)))
}

// resolutionHead renders the card header, naming the decision source on
// closed cards so operators can see where approval came from.
func resolutionHead(verdict, by string, dry bool) string {
	switch verdict {
	case "approve":
		if dry {
			return "✅ <b>Approved (dry run — not executed)</b>"
		}
		return fmt.Sprintf("✅ <b>Approved via %s</b>", html.EscapeString(sourceLabel(by)))
	case "deny":
		return fmt.Sprintf("❌ <b>Denied via %s</b>", html.EscapeString(sourceLabel(by)))
	case "timeout", "confirmation_timeout":
		return "⌛ <b>Expired — timed out</b>"
	case "client_aborted":
		return "⌛ <b>Aborted — caller disconnected</b>"
	case "confirm":
		return "⚠️ <b>Are you sure? Tap again to run</b>"
	default:
		return "🔔 <b>Approval needed</b>"
	}
}

// sourceLabel maps the verdict attribution to a human channel name.
func sourceLabel(by string) string {
	switch {
	case by == "paseo":
		return "Paseo Desktop"
	case strings.HasPrefix(by, "telegram:"):
		return "Telegram (@" + strings.TrimPrefix(by, "telegram:") + ")"
	default:
		return "CLI (" + by + ")"
	}
}

// Buttons builds the inline keyboard; exported so the service can
// re-key an edited card (confirm step reuses the same wire format).
func Buttons(rid string) string {
	return keyboard(rid)
}

func keyboard(rid string) string {
	kb, _ := json.Marshal(Keyboard{Buttons: [][]InlineButton{{
		{Text: "✅ Approve", Data: "approve:" + rid},
		{Text: "❌ Deny", Data: "deny:" + rid},
	}}})
	return string(kb)
}

// AuthButtons builds an inline keyboard with a single URL button opening
// the registry's WebAuthn/2FA ceremony page. URL buttons carry no callback
// data and grant the bot nothing; the operator authenticates directly with
// the registry in their own browser.
func AuthButtons(authURL string) string {
	kb, _ := json.Marshal(struct {
		Buttons [][]struct {
			Text string `json:"text"`
			URL  string `json:"url"`
		} `json:"inline_keyboard"`
	}{Buttons: [][]struct {
		Text string `json:"text"`
		URL  string `json:"url"`
	}{{{Text: "🔑 Open WebAuthn Verification", URL: authURL}}}})
	return string(kb)
}

// maxNotifySummaryRunes bounds the notify summary on cards.
const maxNotifySummaryRunes = 1000

// NotifyCard renders a no-exec notify petition: link + summary with ack
// state. All untrusted fields are html-escaped.
func NotifyCard(host, link, summary, rid string, expiresIn int64, acked bool, ackBy string) string {
	sum := summary
	if r := []rune(sum); len(r) > maxNotifySummaryRunes {
		sum = string(r[:maxNotifySummaryRunes]) + "…[truncated]"
	}
	head := "📣 <b>Action needed</b>"
	if acked {
		head = fmt.Sprintf("✅ <b>Acknowledged via %s</b>", html.EscapeString(sourceLabel(ackBy)))
	}
	return fmt.Sprintf("%s\n🖥️ host: <code>%s</code>\n🔗 link: %s\n📝 summary:\n%s\n🕒 expires in %ds · notify %s",
		head, html.EscapeString(host), html.EscapeString(link), expandablePre(html.EscapeString(sum)), expiresIn, spillID(html.EscapeString(rid)))
}

// NotifyButtons builds the inline keyboard for a notify card: an Ack
// callback button plus a URL button opening the link.
func NotifyButtons(link, rid string) string {
	kb, _ := json.Marshal(struct {
		Inline [][]map[string]string `json:"inline_keyboard"`
	}{Inline: [][]map[string]string{
		{{"text": "✅ Ack", "callback_data": "ack:" + rid}},
		{{"text": "🔗 Open link", "url": link}},
	}})
	return string(kb)
}

// maxAskQuestionRunes bounds the rendered question on ask cards.
const maxAskQuestionRunes = 1000

// maxAskButtonRunes bounds each option button label (Telegram caps button
// text; a long option must never drop the whole keyboard).
const maxAskButtonRunes = 48

// AskCard renders an interactive multi-choice question petition. While
// pending it carries the question and a link; once chosen it shows the
// winning option and its source. All untrusted fields are html-escaped.
func AskCard(host, question, link, rid string, expiresIn int64, chosen bool, selection, by string) string {
	q := question
	if r := []rune(q); len(r) > maxAskQuestionRunes {
		q = string(r[:maxAskQuestionRunes]) + "…[truncated]"
	}
	head := "❓ <b>Question — pick one</b>"
	if chosen {
		head = fmt.Sprintf("✅ <b>Chosen: %s</b>", html.EscapeString(selection))
	}
	card := fmt.Sprintf("%s\n🖥️ host: <code>%s</code>\n📝 question:\n%s",
		head, html.EscapeString(host), expandablePre(html.EscapeString(q)))
	if link != "" {
		card += "\n🔗 link: " + html.EscapeString(link)
	}
	if chosen {
		card += fmt.Sprintf("\n👤 chosen by: <code>%s</code>", html.EscapeString(sourceLabel(by)))
	}
	return fmt.Sprintf("%s\n🕒 expires in %ds · ask %s", card, expiresIn, spillID(html.EscapeString(rid)))
}

// AskButtons builds the inline keyboard for an ask card: one callback
// button per option, data "ask:<rid>:<idx>". RecommendedIndex marks one
// option with a star (best-effort highlight; -1 or out of range = none).
func AskButtons(rid string, options []string, recommended int) string {
	rows := make([][]InlineButton, 0, len(options))
	for i, opt := range options {
		label := fmt.Sprintf("%d. %s", i+1, opt)
		if r := []rune(label); len(r) > maxAskButtonRunes {
			label = string(r[:maxAskButtonRunes]) + "…"
		}
		if i == recommended {
			label = "⭐ " + label
		}
		rows = append(rows, []InlineButton{{
			Text: label,
			Data: "ask:" + rid + ":" + strconv.Itoa(i),
		}})
	}
	kb, _ := json.Marshal(Keyboard{Buttons: rows})
	return string(kb)
}

// PagedPageSize bounds how many list lines ride on a single card page.
const PagedPageSize = 5

// ListPages returns the page count for n lines: always a whole number of
// pages and never less than one, so empty and single-page lists both
// render exactly one (buttonless) page.
func ListPages(n int) int {
	if n <= 0 {
		return 1
	}
	return (n + PagedPageSize - 1) / PagedPageSize
}

// PageSlice returns the lines on 0-based page. Out-of-range pages yield
// nil; callers should clamp against ListPages first.
func PageSlice(lines []string, page int) []string {
	if page < 0 {
		return nil
	}
	start := page * PagedPageSize
	if start >= len(lines) {
		return nil
	}
	end := start + PagedPageSize
	if end > len(lines) {
		end = len(lines)
	}
	return lines[start:end]
}

// PagedButtons builds the pager row for page (0-based) of pages. Prev/Next
// are hidden at the boundaries and single-page or empty lists get no
// buttons at all. The centre label is a no-op so the current position is
// always visible.
func PagedButtons(rid string, page, pages int) string {
	if pages <= 1 {
		return EmptyButtons()
	}
	if page < 0 {
		page = 0
	}
	if page >= pages {
		page = pages - 1
	}
	nav := make([]InlineButton, 0, 3)
	if page > 0 {
		nav = append(nav, InlineButton{Text: "◀️ Prev", Data: "page:" + rid + ":" + strconv.Itoa(page-1)})
	}
	nav = append(nav, InlineButton{Text: fmt.Sprintf("Page %d/%d", page+1, pages), Data: "noop"})
	if page+1 < pages {
		nav = append(nav, InlineButton{Text: "Next ▶️", Data: "page:" + rid + ":" + strconv.Itoa(page+1)})
	}
	kb, _ := json.Marshal(Keyboard{Buttons: [][]InlineButton{nav}})
	return string(kb)
}

// PagedCard renders one page of a long list. host/title/lines are
// untrusted and escaped; an empty page renders an explicit marker so the
// operator never sees a blank card. Pair with PagedButtons.
func PagedCard(host, title, rid string, lines []string, page, pages int) string {
	body := "No items."
	if len(lines) > 0 {
		esc := make([]string, len(lines))
		for i, l := range lines {
			esc[i] = html.EscapeString(l)
		}
		body = "<pre><code>" + strings.Join(esc, "\n") + "</code></pre>"
	}
	return fmt.Sprintf("📄 <b>%s</b>\n🖥️ host: <code>%s</code>\n%s\n📑 page %d/%d · list %s",
		html.EscapeString(title), html.EscapeString(host), body, page+1, pages, spillID(html.EscapeString(rid)))
}

// Send posts the card, returns the message id for later rewriting.
func (c Client) Send(chatID, text, rid string) (int64, error) {
	return c.SendWithMarkup(chatID, text, 0, keyboard(rid))
}

// SendWithMarkup posts text with caller-supplied inline markup, threading
// it as a reply to replyTo when nonzero (used for 2FA challenge alerts
// attached to the approval card thread).
func (c Client) SendWithMarkup(chatID, text string, replyTo int64, markup string) (int64, error) {
	v := url.Values{
		"chat_id":      {chatID},
		"text":         {text},
		"parse_mode":   {"HTML"},
		"reply_markup": {markup},
	}
	if replyTo != 0 {
		v.Set("reply_to_message_id", strconv.FormatInt(replyTo, 10))
	}
	body, err := c.call("sendMessage", v)
	if err != nil {
		return 0, err
	}
	return parseMsgID(body)
}

func parseMsgID(body []byte) (int64, error) {
	var r apiResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return 0, err
	}
	if !r.OK {
		return 0, fmt.Errorf("telegram: !ok")
	}
	return r.Result.ID, nil
}

// Edit rewrites a posted card. Pass Buttons(rid) to keep live buttons
// (confirm step) or EmptyButtons() to close it.
func (c Client) Edit(chatID string, msgID int64, text, markup string) {
	_, _ = c.call("editMessageText", url.Values{
		"chat_id":      {chatID},
		"message_id":   {strconv.FormatInt(msgID, 10)},
		"text":         {text},
		"parse_mode":   {"HTML"},
		"reply_markup": {markup},
	})
}

// EmptyButtons strips all buttons (final states).
func EmptyButtons() string {
	empty, _ := json.Marshal(Keyboard{Buttons: [][]InlineButton{}})
	return string(empty)
}

func (c Client) answer(cbID, text string) {
	_, _ = c.call("answerCallbackQuery", url.Values{
		"callback_query_id": {cbID},
		"text":              {text},
	})
}

// Verdict is one parsed approval callback.
type Verdict struct {
	RID      string
	Decision string
	By       string
	// Idx is the chosen option index for ask callbacks; unused otherwise.
	Idx int
}

// Poll loops getUpdates (outbound long-poll, no open ports) and delivers
// approved-sender callbacks for request ids. Exits when ctx is canceled.
func (c Client) Poll(ctx context.Context, offset int64, approvers map[string]bool, out chan<- Verdict, save func(int64)) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		body, err := c.callContext(ctx, "getUpdates", url.Values{
			"offset":  {strconv.FormatInt(offset, 10)},
			"timeout": {"30"},
		})
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}
		var up apiUpdates
		if err := json.Unmarshal(body, &up); err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}
		for _, u := range up.Result {
			offset = u.ID + 1
			data := u.Callback.Data
			by := strconv.FormatInt(u.Callback.From.ID, 10)
			if !approvers[by] {
				continue
			}
			kind, rid, idx, ok := splitVerdict(data)
			if !ok {
				if data == "noop" {
					c.answer(u.Callback.ID, "")
				}
				continue
			}
			select {
			case out <- Verdict{RID: rid, Decision: kind, By: by, Idx: idx}:
			case <-ctx.Done():
				return
			}
			switch kind {
			case "approve":
				c.answer(u.Callback.ID, "approved — executing")
			case "ack":
				c.answer(u.Callback.ID, "acknowledged")
			case "ask":
				c.answer(u.Callback.ID, "answer recorded")
			case "page":
				c.answer(u.Callback.ID, fmt.Sprintf("page %d", idx+1))
			default:
				c.answer(u.Callback.ID, "denied")
			}
		}
		save(offset)
	}
}

func splitVerdict(data string) (kind, rid string, idx int, ok bool) {
	if strings.HasPrefix(data, "ask:") {
		rest := strings.TrimPrefix(data, "ask:")
		i := strings.LastIndex(rest, ":")
		if i <= 0 || i == len(rest)-1 {
			return "", "", 0, false
		}
		n, err := strconv.Atoi(rest[i+1:])
		if err != nil || n < 0 {
			return "", "", 0, false
		}
		return "ask", rest[:i], n, true
	}
	if strings.HasPrefix(data, "page:") {
		rest := strings.TrimPrefix(data, "page:")
		i := strings.LastIndex(rest, ":")
		if i <= 0 || i == len(rest)-1 {
			return "", "", 0, false
		}
		n, err := strconv.Atoi(rest[i+1:])
		if err != nil || n < 0 {
			return "", "", 0, false
		}
		return "page", rest[:i], n, true
	}
	if strings.HasPrefix(data, "approve:") {
		return "approve", strings.TrimPrefix(data, "approve:"), 0, true
	}
	if strings.HasPrefix(data, "deny:") {
		return "deny", strings.TrimPrefix(data, "deny:"), 0, true
	}
	if strings.HasPrefix(data, "ack:") {
		return "ack", strings.TrimPrefix(data, "ack:"), 0, true
	}
	return "", "", 0, false
}
