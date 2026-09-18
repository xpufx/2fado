package service

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"2fado/internal/config"
	"2fado/internal/policy"
	"2fado/internal/protocol"
)

// TestRepageEditsStoredCardInPlace drives the TelegramPump paging handler
// end to end: a stored list card re-renders the requested page in place
// and ignores out-of-range pages.
func TestRepageEditsStoredCardInPlace(t *testing.T) {
	var gotText, gotMarkup string
	var edits int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if strings.HasSuffix(r.URL.Path, "/editMessageText") {
			atomic.AddInt32(&edits, 1)
			gotText = r.Form.Get("text")
			gotMarkup = r.Form.Get("reply_markup")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"ok":true,"result":{"message_id":7,"chat":{"id":1}}}`)
	}))
	defer ts.Close()

	svc := New(config.Conf{StateDir: t.TempDir(), Timeout: 5, BotToken: "test-token"})
	svc.TG.BaseURL = ts.URL
	if svc.getState() != nil {
		svc.getState().tgClient.BaseURL = ts.URL
	}
	if err := svc.Store.Init(); err != nil {
		t.Fatal(err)
	}
	rid := "list-1"
	lines := []string{"a", "b", "c", "d", "e", "f"}
	if err := svc.Store.Save(protocol.PendingRecord{
		UID: 1000, Expires: time.Now().Add(time.Hour).Unix(), Kind: "notify",
	}, rid); err != nil {
		t.Fatal(err)
	}
	svc.Store.AttachPager(rid, "123", 7)
	svc.Store.SetPageList(rid, "Recent", lines)

	svc.repage(rid, 1)
	if got := atomic.LoadInt32(&edits); got != 1 {
		t.Fatalf("edits = %d, want 1", got)
	}
	if !strings.Contains(gotText, "Recent") || !strings.Contains(gotText, "page 2/2") || !strings.Contains(gotText, "f") {
		t.Fatalf("edited page text =\n%s", gotText)
	}
	if !strings.Contains(gotMarkup, "page:list-1:0") || strings.Contains(gotMarkup, "page:list-1:2") {
		t.Fatalf("edited page markup = %s", gotMarkup)
	}

	// Out-of-range pages are ignored (idempotent, no double render).
	svc.repage(rid, 9)
	if got := atomic.LoadInt32(&edits); got != 1 {
		t.Fatalf("out-of-range page edited: %d", got)
	}
}

// TestSendListRequiresStoredRecord keeps the producer honest: paging a
// rid with no backing record must fail rather than post an orphan card.
func TestSendListRequiresStoredRecord(t *testing.T) {
	svc := testService(t, policy.Policy{Default: "ask"})
	if _, err := svc.SendList("missing-rid", "T", []string{"x"}); err == nil {
		t.Fatal("SendList on an unknown rid must return an error")
	}
}
