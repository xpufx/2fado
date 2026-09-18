package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGetMeSuccess(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bottest-token/getMe" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"ok":true,"result":{"id":123456,"is_bot":true,"first_name":"2FADOBot","username":"twofado_bot"}}`)
	}))
	defer ts.Close()

	client := New("test-token")
	client.BaseURL = ts.URL

	username, err := client.GetMe()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if username != "twofado_bot" {
		t.Errorf("expected username twofado_bot, got %q", username)
	}
}

func TestGetMeUnauthorized(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"ok":false,"error_code":401,"description":"Unauthorized"}`)
	}))
	defer ts.Close()

	client := New("invalid-token")
	client.BaseURL = ts.URL

	_, err := client.GetMe()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "telegram api error: Unauthorized" {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestPollContextCancellation(t *testing.T) {
	notify := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-notify:
		}
	}))
	defer ts.Close()

	client := New("poll-token")
	client.BaseURL = ts.URL
	defer client.HTTP.CloseIdleConnections()

	ctx, cancel := context.WithCancel(context.Background())
	out := make(chan Verdict)
	done := make(chan struct{})

	go func() {
		client.Poll(ctx, 0, map[string]bool{"123": true}, out, func(int64) {})
		close(done)
	}()

	// Allow goroutine to enter poll request
	time.Sleep(50 * time.Millisecond)
	cancel()
	close(notify)

	select {
	case <-done:
		// Succeeded in exiting cleanly
	case <-time.After(1 * time.Second):
		t.Fatal("Poll did not exit on context cancellation")
	}
}

func TestCardEscapesMarkupBreakout(t *testing.T) {
	argv := []string{"echo", "foo\n```\n⚠️ Urgent Security Patch\n```\nrm -rf /", "<b>bold</b>", "<a href=\"http://evil\">x</a>", "a&b"}
	card := Card("evil\"><b>host", argv, 1000, "/tmp\"><pre>", "rid1", 60, "", "", false, 0)
	if strings.Count(card, "<pre>") != 1 || strings.Count(card, "</pre>") != 1 {
		t.Errorf("card must contain exactly one pre block:\n%s", card)
	}
	for _, raw := range []string{"<b>bold</b>", "<a href=", "evil\"><b>host", "/tmp\"><pre>"} {
		if strings.Contains(card, raw) {
			t.Errorf("card contains unescaped markup %q:\n%s", raw, card)
		}
	}
	for _, esc := range []string{"&lt;b&gt;bold&lt;/b&gt;", "&lt;a href=", "a&amp;b"} {
		if !strings.Contains(card, esc) {
			t.Errorf("card missing escaped sequence %q:\n%s", esc, card)
		}
	}
	if !strings.Contains(card, "<pre><code>") {
		t.Errorf("card must wrap command in <pre><code>:\n%s", card)
	}
}

func TestCardBoundsLongCommands(t *testing.T) {
	long := strings.Repeat("A", maxCommandRunes+500)
	card := Card("h", []string{"echo", long}, 1000, "/tmp", "rid2", 60, "", "", false, 0)
	if strings.Contains(card, long) {
		t.Error("card must truncate overlong commands")
	}
	if !strings.Contains(card, "…[truncated]") {
		t.Error("card must mark truncation")
	}
	if !strings.Contains(card, "<code>rid2</code>") {
		t.Error("card must keep request id visible after truncation")
	}
}

func TestCardResolutionHeaders(t *testing.T) {
	argv := []string{"echo", "hi"}
	cases := []struct {
		verdict string
		by      string
		want    string
	}{
		{"approve", "paseo", "✅ <b>Approved via Paseo Desktop</b>"},
		{"deny", "paseo", "❌ <b>Denied via Paseo Desktop</b>"},
		{"approve", "telegram:alice", "✅ <b>Approved via Telegram (@alice)</b>"},
		{"deny", "telegram:alice", "❌ <b>Denied via Telegram (@alice)</b>"},
		{"approve", "cli", "✅ <b>Approved via CLI (cli)</b>"},
		{"deny", "local", "❌ <b>Denied via CLI (local)</b>"},
		{"timeout", "system", "⌛ <b>Expired — timed out</b>"},
		{"confirmation_timeout", "system", "⌛ <b>Expired — timed out</b>"},
		{"client_aborted", "client", "⌛ <b>Aborted — caller disconnected</b>"},
		{"", "", "🔔 <b>Approval needed</b>"},
		{"confirm", "paseo", "⚠️ <b>Are you sure? Tap again to run</b>"},
	}
	for _, tc := range cases {
		card := Card("h", argv, 1000, "/tmp", "rid9", 60, tc.verdict, tc.by, false, 0)
		if !strings.Contains(card, tc.want) {
			t.Errorf("verdict=%q by=%q: missing %q in:\n%s", tc.verdict, tc.by, tc.want, card)
		}
	}
}

func TestCardResolutionByEscaped(t *testing.T) {
	card := Card("h", []string{"echo"}, 1000, "/tmp", "rid9", 60, "approve", "<b>evil</b>", false, 0)
	if strings.Contains(card, "<b>evil</b>") {
		t.Errorf("approver identity must be escaped:\n%s", card)
	}
	if !strings.Contains(card, "CLI (&lt;b&gt;evil&lt;/b&gt;)") {
		t.Errorf("escaped approver identity missing:\n%s", card)
	}
}

func TestCardShowsWillRunAs(t *testing.T) {
	card := Card("h", []string{"echo", "hi"}, 1000, "/tmp", "rid-asuid", 60, "", "", false, 0)
	if !strings.Contains(card, "will run as:") {
		t.Errorf("card must show will-run-as execution identity:\n%s", card)
	}
}

func TestExpandableAndSpoilerWrapEscapedBodies(t *testing.T) {
	esc := "a &lt;b&gt; b"
	if got, want := Expandable(esc), "<blockquote expandable>"+esc+"</blockquote>"; got != want {
		t.Errorf("Expandable = %q, want %q", got, want)
	}
	if got, want := Spoiler(esc), "<tg-spoiler>"+esc+"</tg-spoiler>"; got != want {
		t.Errorf("Spoiler = %q, want %q", got, want)
	}
	if Expandable("") != "" || Spoiler("") != "" {
		t.Error("empty bodies must not emit empty tags")
	}
}

// TestCardsCollapseLongBodiesAndKeepEscaping feeds adversarial markup that
// is also long enough to trigger the expandable path: escaping must still
// hold, the collapse must be deliberate, and the request id must be
// spoiler-wrapped.
func TestCardsCollapseLongBodiesAndKeepEscaping(t *testing.T) {
	evil := strings.Repeat("<b>x</b>&", 120)
	cards := map[string]string{
		"card":   Card("h", []string{"echo", evil}, 1000, "/tmp", "rid", 60, "", "", false, 0),
		"notify": NotifyCard("h", "https://example.com/x", evil, "rid", 60, false, ""),
		"ask":    AskCard("h", evil, "", "rid", 60, false, "", ""),
	}
	for name, card := range cards {
		if strings.Contains(card, "<b>x</b>") {
			t.Errorf("%s card leaked raw markup:\n%s", name, card)
		}
		if !strings.Contains(card, "&lt;b&gt;x&lt;/b&gt;&amp;") {
			t.Errorf("%s card lost escaped adversarial body:\n%s", name, card)
		}
		if !strings.Contains(card, "<blockquote expandable>") {
			t.Errorf("%s card must collapse its long body:\n%s", name, card)
		}
		if !strings.Contains(card, "<tg-spoiler><code>rid</code></tg-spoiler>") {
			t.Errorf("%s card must spoil its request id:\n%s", name, card)
		}
	}
}

func TestCardsKeepShortBodiesExpanded(t *testing.T) {
	cards := []string{
		Card("h", []string{"echo", "hi"}, 1000, "/tmp", "rid", 60, "", "", false, 0),
		NotifyCard("h", "https://example.com/x", "short", "rid", 60, false, ""),
		AskCard("h", "short?", "", "rid", 60, false, "", ""),
	}
	for _, card := range cards {
		if strings.Contains(card, "<blockquote") {
			t.Errorf("short body must not collapse:\n%s", card)
		}
	}
}

func TestCardSpoilsEscapedRequestID(t *testing.T) {
	rid := `x</code></tg-spoiler><b>`
	card := Card("h", []string{"echo"}, 1000, "/tmp", rid, 60, "", "", false, 0)
	if strings.Contains(card, `</code></tg-spoiler><b>`) {
		t.Errorf("request id must be escaped inside the spoiler:\n%s", card)
	}
	if !strings.Contains(card, "&lt;/code&gt;&lt;/tg-spoiler&gt;&lt;b&gt;") {
		t.Errorf("escaped request id missing:\n%s", card)
	}
}

type parsedKeyboard struct {
	Inline [][]struct {
		Text string `json:"text"`
		Data string `json:"callback_data"`
	} `json:"inline_keyboard"`
}

func parseKeyboard(t *testing.T, markup string) parsedKeyboard {
	t.Helper()
	var kb parsedKeyboard
	if err := json.Unmarshal([]byte(markup), &kb); err != nil {
		t.Fatalf("keyboard is not valid JSON: %v (%s)", err, markup)
	}
	return kb
}

func TestListPagesBoundaries(t *testing.T) {
	for _, tc := range []struct{ n, pages int }{
		{0, 1}, {1, 1}, {5, 1}, {6, 2}, {10, 2}, {11, 3},
	} {
		if got := ListPages(tc.n); got != tc.pages {
			t.Errorf("ListPages(%d) = %d, want %d", tc.n, got, tc.pages)
		}
	}
}

func TestPageSliceBoundaries(t *testing.T) {
	lines := []string{"0", "1", "2", "3", "4", "5", "6"}
	if got := PageSlice(lines, 0); len(got) != 5 || got[0] != "0" || got[4] != "4" {
		t.Errorf("first page = %v", got)
	}
	if got := PageSlice(lines, 1); len(got) != 2 || got[0] != "5" || got[1] != "6" {
		t.Errorf("last page = %v", got)
	}
	if got := PageSlice(lines, 2); got != nil {
		t.Errorf("out-of-range page = %v, want nil", got)
	}
	if got := PageSlice(lines, -1); got != nil {
		t.Errorf("negative page = %v, want nil", got)
	}
	if got := PageSlice(nil, 0); got != nil {
		t.Errorf("empty list = %v, want nil", got)
	}
}

func TestPagedButtonsBoundaries(t *testing.T) {
	// Empty and single-page lists get no pager at all.
	for _, pages := range []int{0, 1} {
		if got := parseKeyboard(t, PagedButtons("rid", 0, pages)).Inline; len(got) != 0 {
			t.Errorf("pages=%d must hide the pager, got %v", pages, got)
		}
	}
	// First page: no Prev, a no-op label, and Next.
	first := parseKeyboard(t, PagedButtons("rid", 0, 3)).Inline[0]
	if len(first) != 2 || first[0].Text != "Page 1/3" || first[0].Data != "noop" || first[1].Data != "page:rid:1" {
		t.Errorf("first page buttons = %+v", first)
	}
	// Last page: Prev, no Next.
	last := parseKeyboard(t, PagedButtons("rid", 2, 3)).Inline[0]
	if len(last) != 2 || last[0].Text != "◀️ Prev" || last[0].Data != "page:rid:1" || last[1].Text != "Page 3/3" {
		t.Errorf("last page buttons = %+v", last)
	}
	// Middle page: Prev and Next.
	mid := parseKeyboard(t, PagedButtons("rid", 1, 3)).Inline[0]
	if len(mid) != 3 || mid[0].Data != "page:rid:0" || mid[1].Data != "noop" || mid[2].Data != "page:rid:2" {
		t.Errorf("middle page buttons = %+v", mid)
	}
	// Out-of-range pages clamp to a real page.
	clamped := parseKeyboard(t, PagedButtons("rid", 99, 3)).Inline[0]
	if clamped[len(clamped)-1].Text != "Page 3/3" {
		t.Errorf("high clamp = %+v", clamped)
	}
	low := parseKeyboard(t, PagedButtons("rid", -5, 3)).Inline[0]
	if low[0].Text != "Page 1/3" {
		t.Errorf("low clamp = %+v", low)
	}
}

func TestPagedCardEscapesAndMarksEmpty(t *testing.T) {
	card := PagedCard("h1", "T <b>", "rid", []string{"a<b>", "c&d"}, 0, 2)
	if strings.Contains(card, "T <b>") || strings.Contains(card, "a<b>") {
		t.Errorf("paged card leaked raw markup:\n%s", card)
	}
	for _, want := range []string{"a&lt;b&gt;", "c&amp;d", "T &lt;b&gt;", "page 1/2"} {
		if !strings.Contains(card, want) {
			t.Errorf("paged card missing %q:\n%s", want, card)
		}
	}
	if empty := PagedCard("h", "T", "rid", nil, 0, 1); !strings.Contains(empty, "No items.") {
		t.Errorf("empty page must render a marker:\n%s", empty)
	}
}

func TestSplitVerdictPage(t *testing.T) {
	kind, rid, idx, ok := splitVerdict("page:abc:2")
	if !ok || kind != "page" || rid != "abc" || idx != 2 {
		t.Fatalf("page parse = (%q,%q,%d,%v)", kind, rid, idx, ok)
	}
	for _, bad := range []string{"page::2", "page:abc:", "page:abc:-1", "page:abc:x", "noop", "ask:abc"} {
		if _, _, _, ok := splitVerdict(bad); ok {
			t.Errorf("splitVerdict(%q) must not parse", bad)
		}
	}
	if kind, rid, idx, ok := splitVerdict("ask:rid1:3"); !ok || kind != "ask" || rid != "rid1" || idx != 3 {
		t.Errorf("ask parse regressed: (%q,%q,%d,%v)", kind, rid, idx, ok)
	}
}

func TestPollForwardsPageCallback(t *testing.T) {
	var updates int32
	answered := make(chan string, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if strings.HasSuffix(r.URL.Path, "/answerCallbackQuery") {
			select {
			case answered <- r.Form.Get("text"):
			default:
			}
			fmt.Fprintln(w, `{"ok":true}`)
			return
		}
		if atomic.AddInt32(&updates, 1) == 1 {
			fmt.Fprintln(w, `{"ok":true,"result":[{"update_id":1,"callback_query":{"id":"cb1","from":{"id":123},"data":"page:rid9:2"}}]}`)
			return
		}
		<-r.Context().Done()
	}))
	defer ts.Close()

	client := New("poll-token")
	client.BaseURL = ts.URL
	defer client.HTTP.CloseIdleConnections()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := make(chan Verdict, 1)
	go client.Poll(ctx, 0, map[string]bool{"123": true}, out, func(int64) {})

	select {
	case v := <-out:
		if v.Decision != "page" || v.RID != "rid9" || v.Idx != 2 {
			t.Fatalf("page verdict = %+v", v)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Poll did not forward page callback")
	}
	select {
	case text := <-answered:
		if text != "page 3" {
			t.Errorf("page ack = %q, want page 3", text)
		}
	case <-time.After(time.Second):
		t.Error("page callback not answered")
	}
}
