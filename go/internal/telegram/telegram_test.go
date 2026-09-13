package telegram

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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
	card := Card("evil\"><b>host", argv, 1000, "/tmp\"><pre>", "rid1", 60, "", false)
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
	card := Card("h", []string{"echo", long}, 1000, "/tmp", "rid2", 60, "", false)
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
