// Package telegram speaks Bot API with typed structs only. It renders
// the approval card and runs the outbound long-poll verdict loop.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	if _, err := b.ReadFrom(resp.Body); err != nil {
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

// Card renders the approval request. Verdict is "" while pending,
// "confirm" for the second-step challenge.
func Card(host string, argv []string, uid uint32, cwd, rid string, expiresIn int64, verdict string, dry bool) string {
	var head string
	switch verdict {
	case "approve":
		head = "✅ *Approved*"
		if dry {
			head = "✅ *Approved (dry run — not executed)*"
		}
	case "deny":
		head = "❌ *Denied*"
	case "timeout":
		head = "⌛ *Expired — denied*"
	case "confirm":
		head = "⚠️ *Are you sure? Tap again to run*"
	default:
		head = "🔔 *Approval needed*"
	}
	return fmt.Sprintf("%s\n🖥️ host: `%s`\n👤 caller: `%s`\n📁 cwd: `%s`\n⌨️ command:\n```\n%s\n```\n🕒 expires in %ds · request `%s`",
		head, host, Who(uid), cwd, strings.Join(argv, " "), expiresIn, rid)
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

// Send posts the card, returns the message id for later rewriting.
func (c Client) Send(chatID, text, rid string) (int64, error) {
	body, err := c.call("sendMessage", url.Values{
		"chat_id":      {chatID},
		"text":         {text},
		"parse_mode":   {"Markdown"},
		"reply_markup": {keyboard(rid)},
	})
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
		"parse_mode":   {"Markdown"},
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
			kind, rid, ok := splitVerdict(data)
			if !ok {
				continue
			}
			by := strconv.FormatInt(u.Callback.From.ID, 10)
			if !approvers[by] {
				continue
			}
			select {
			case out <- Verdict{RID: rid, Decision: kind, By: by}:
			case <-ctx.Done():
				return
			}
			if kind == "approve" {
				c.answer(u.Callback.ID, "approved — executing")
			} else {
				c.answer(u.Callback.ID, "denied")
			}
		}
		save(offset)
	}
}

func splitVerdict(data string) (kind, rid string, ok bool) {
	if strings.HasPrefix(data, "approve:") {
		return "approve", strings.TrimPrefix(data, "approve:"), true
	}
	if strings.HasPrefix(data, "deny:") {
		return "deny", strings.TrimPrefix(data, "deny:"), true
	}
	return "", "", false
}
