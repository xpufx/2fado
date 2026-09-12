package telegram

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
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
