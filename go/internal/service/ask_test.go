package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"2fado/internal/config"
	"2fado/internal/policy"
	"2fado/internal/protocol"
	"2fado/internal/telegram"
)

// TestAskRoundTripSelection drives a full ask round trip: create the
// petition, find its id, simulate the operator callback, and assert the
// blocking call resolves with the chosen option.
func TestAskRoundTripSelection(t *testing.T) {
	svc := testService(t, policy.Policy{Default: "ask"})
	done := make(chan protocol.AskResult, 1)
	go func() {
		done <- svc.Ask(protocol.AskRequest{
			Question:   "Choose wire-format for x-comms idempotency (#184):",
			Options:    []string{"Option 3: Daemon messageId dedup", "Option 2: Peer-RPC seen-id LRU", "Option 1: Envelope v5"},
			Link:       "https://example.com/184",
			TTLSeconds: 30,
		}, 1000)
	}()

	rid := waitForAsk(t, svc, 3*time.Second)
	if !svc.Store.Select(rid, "Option 2: Peer-RPC seen-id LRU", 1, "telegram:42") {
		t.Fatal("Select on pending ask must be accepted")
	}
	if svc.Store.Select(rid, "Option 1: Envelope v5", 2, "telegram:99") {
		t.Fatal("second selection must not win (first selection wins)")
	}

	select {
	case res := <-done:
		if res.Status != "selected" || res.ID != rid {
			t.Fatalf("Ask = %+v, want selected %s", res, rid)
		}
		if res.Selection != "Option 2: Peer-RPC seen-id LRU" || res.SelectionIdx != 1 || res.By != "telegram:42" {
			t.Fatalf("Ask selection = %+v", res)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Ask did not return after selection")
	}

	st := svc.Status(rid)
	if st.Status != "selected" || st.Decision != "select" || st.Kind != "ask" {
		t.Fatalf("Status = %+v, want selected ask", st)
	}
	if st.Selection != "Option 2: Peer-RPC seen-id LRU" || st.SelectionIdx != 1 || st.By != "telegram:42" {
		t.Fatalf("Status selection = %+v", st)
	}
	if len(st.Options) != 3 || st.Question == "" {
		t.Fatalf("Status must carry question+options: %+v", st)
	}
	// Stored record carries the selection state and the future-matrix fields.
	rec, err := svc.Store.Load(rid)
	if err != nil {
		t.Fatal(err)
	}
	if rec.MultiSelect || rec.AllowWriteIn || rec.RecommendedIndex != 0 {
		t.Fatalf("MVP record matrix fields = %+v, want zero values persisted", rec)
	}
	if items := svc.List().Items; len(items) != 0 {
		t.Fatalf("selected ask must clear pending list, got %+v", items)
	}
	found := false
	for _, it := range svc.Recent(10).Items {
		if it.ID == rid {
			found = true
			if it.Decision != "select" || it.Selection != "Option 2: Peer-RPC seen-id LRU" || it.SelectionIdx != 1 {
				t.Fatalf("recent ask item = %+v", it)
			}
		}
	}
	if !found {
		t.Fatal("selected ask missing from recent history")
	}
}

// TestAskValidation rejects malformed petitions before any record is written.
func TestAskValidation(t *testing.T) {
	svc := testService(t, policy.Policy{Default: "ask"})
	cases := []protocol.AskRequest{
		{Question: "", Options: []string{"a", "b"}},
		{Question: "q", Options: []string{"only-one"}},
		{Question: "q", Options: []string{"a", "b"}, Link: "ftp://example.com/x"},
	}
	for _, req := range cases {
		if res := svc.Ask(req, 1000); res.Status != "denied" {
			t.Errorf("Ask(%+v) = %+v, want denied", req, res)
		}
	}
	if items := svc.List().Items; len(items) != 0 {
		t.Errorf("invalid asks created %d records", len(items))
	}
}

// TestAskTTLClampAndCancel bounds the wait: a tiny TTL clamps up to the
// minimum, and a disconnected caller unblocks without waiting it out.
func TestAskTTLClampAndCancel(t *testing.T) {
	svc := testService(t, policy.Policy{Default: "ask"})
	cancel := make(chan struct{})
	done := make(chan protocol.AskResult, 1)
	go func() {
		done <- svc.AskWithCancel(protocol.AskRequest{
			Question: "q", Options: []string{"a", "b"}, TTLSeconds: 1,
		}, 1000, cancel)
	}()
	rid := waitForAsk(t, svc, 3*time.Second)
	rec, err := svc.Store.Load(rid)
	if err != nil {
		t.Fatal(err)
	}
	if ttl := time.Until(time.Unix(rec.Expires, 0)); ttl < MinAskTTL-2*time.Second {
		t.Fatalf("min clamp TTL = %v, want >= %v", ttl, MinAskTTL)
	}
	close(cancel)
	select {
	case res := <-done:
		if res.Status != "denied" || res.Reason != "client_aborted" {
			t.Fatalf("cancelled Ask = %+v, want denied client_aborted", res)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled Ask did not return")
	}
}

// TestAwaitSelectionExpiryBound covers the timeout edge directly: expiry
// returns (nil,false) and cancellation returns (nil,true).
func TestAwaitSelectionExpiryBound(t *testing.T) {
	svc := testService(t, policy.Policy{Default: "ask"})
	rid := "ask-expiry-1"
	if err := svc.Store.Save(protocol.PendingRecord{
		Kind: "ask", Question: "q", Options: []string{"a", "b"},
		Expires: time.Now().Add(time.Hour).Unix(),
	}, rid); err != nil {
		t.Fatal(err)
	}
	if sel, aborted := svc.awaitSelection(rid, time.Now().Add(50*time.Millisecond), nil); sel != nil || aborted {
		t.Fatalf("awaitSelection = (%+v, %v), want (nil,false) on expiry", sel, aborted)
	}
	closed := make(chan struct{})
	close(closed)
	if sel, aborted := svc.awaitSelection(rid, time.Now().Add(time.Hour), closed); sel != nil || !aborted {
		t.Fatalf("awaitSelection = (%+v, %v), want (nil,true) on cancel", sel, aborted)
	}
}

// TestLegacyRecordsWithoutOptionsLoad guards the migration contract: old
// on-disk records that predate ask (no options/question) must still load
// and answer status without error.
func TestLegacyRecordsWithoutOptionsLoad(t *testing.T) {
	svc := testService(t, policy.Policy{Default: "ask"})
	legacy := `{"argv":["/bin/echo","hi"],"uid":1000,"cwd":"/tmp","expires":` +
		"9999999999" + `,"kind":"notify","link":"https://example.com/x","summary":"old"}`
	path := filepath.Join(svc.Store.Pending, "legacy-1.json")
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	rec, err := svc.Store.Load("legacy-1")
	if err != nil {
		t.Fatalf("legacy record must load: %v", err)
	}
	if rec.Kind != "notify" || rec.Link == "" || rec.Options != nil {
		t.Fatalf("legacy record = %+v", rec)
	}
	if st := svc.Status("legacy-1"); st.Status != "pending" || st.Kind != "notify" {
		t.Fatalf("legacy status = %+v", st)
	}
	// An ask-shaped record written empty (no options) must not panic.
	if err := svc.Store.Save(protocol.PendingRecord{Kind: "ask", Expires: time.Now().Add(time.Hour).Unix()}, "ask-empty"); err != nil {
		t.Fatal(err)
	}
	if st := svc.Status("ask-empty"); st.Status != "pending" || st.Kind != "ask" {
		t.Fatalf("empty ask status = %+v", st)
	}
}

// TestAskCardRendersQuestionOptionsAndChoice checks the Telegram render
// surface: card text, callback data, and the chosen-state header.
func TestAskCardRendersQuestionOptionsAndChoice(t *testing.T) {
	card := telegram.AskCard("host1", "Which <b>format</b>?", "https://example.com/x", "rid1", 60, false, "", "", false)
	if !strings.Contains(card, "Which") || strings.Contains(card, "<b>format</b>") {
		t.Fatalf("question must render escaped:\n%s", card)
	}
	chosen := telegram.AskCard("host1", "q", "", "rid1", 0, true, "Option 2: LRU", "telegram:42", false)
	if !strings.Contains(chosen, "Chosen: Option 2: LRU") {
		t.Fatalf("chosen card missing selection:\n%s", chosen)
	}
	btns := telegram.AskButtons("rid1", []string{"first", "second", "third"}, 1, false, nil)
	for _, want := range []string{`ask:rid1:0`, `ask:rid1:1`, `ask:rid1:2`, "⭐ 2. second"} {
		if !strings.Contains(btns, want) {
			t.Fatalf("ask buttons missing %q:\n%s", want, btns)
		}
	}
	var kb struct {
		Inline [][]struct {
			Text string `json:"text"`
			Data string `json:"callback_data"`
		} `json:"inline_keyboard"`
	}
	if err := json.Unmarshal([]byte(btns), &kb); err != nil || len(kb.Inline) != 3 {
		t.Fatalf("ask buttons = %s (err %v), want 3 rows", btns, err)
	}
}

// TestTelegramMultiSelectAccumulatesUntilSubmit drives the pump path: two
// toggles accumulate scratch state, nothing finalizes, then Submit records
// the whole joined set and unblocks the ask caller.
func TestTelegramMultiSelectAccumulatesUntilSubmit(t *testing.T) {
	svc := testService(t, policy.Policy{Default: "ask"})
	go svc.TelegramPump()
	done := make(chan protocol.AskResult, 1)
	go func() {
		done <- svc.Ask(protocol.AskRequest{
			Question:    "Pick colors",
			Options:     []string{"red", "green", "blue"},
			MultiSelect: true,
			TTLSeconds:  30,
		}, 1000)
	}()
	rid := waitForAsk(t, svc, 3*time.Second)

	svc.Verdicts <- telegram.Verdict{RID: rid, Decision: "asktoggle", By: "42", Idxs: []int{0}}
	waitForSelIdxs(t, svc, rid, []int{0})
	svc.Verdicts <- telegram.Verdict{RID: rid, Decision: "asktoggle", By: "42", Idxs: []int{2}}
	waitForSelIdxs(t, svc, rid, []int{0, 2})
	svc.Verdicts <- telegram.Verdict{RID: rid, Decision: "asktoggle", By: "42", Idxs: []int{0}}
	waitForSelIdxs(t, svc, rid, []int{2})
	svc.Verdicts <- telegram.Verdict{RID: rid, Decision: "asktoggle", By: "42", Idxs: []int{1}}
	waitForSelIdxs(t, svc, rid, []int{1, 2})

	// No Submit yet: the blocking ask must still be waiting.
	select {
	case res := <-done:
		t.Fatalf("ask finalized before submit: %+v", res)
	case <-time.After(300 * time.Millisecond):
	}

	svc.Verdicts <- telegram.Verdict{RID: rid, Decision: "asksubmit", By: "42"}
	select {
	case res := <-done:
		if res.Status != "selected" {
			t.Fatalf("multi ask = %+v, want selected", res)
		}
		if res.Selection != "green, blue" {
			t.Fatalf("joined selection = %q, want %q", res.Selection, "green, blue")
		}
		if len(res.SelectionIdxs) != 2 || res.SelectionIdxs[0] != 1 || res.SelectionIdxs[1] != 2 {
			t.Fatalf("selection_idxs = %v, want [1 2]", res.SelectionIdxs)
		}
		if res.SelectionIdx != 0 {
			t.Fatalf("multi SelectionIdx = %d, want 0", res.SelectionIdx)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("multi ask did not resolve after submit")
	}
}

// TestTelegramSingleSelectStillFinalizesOnTap guards the unchanged
// single-select contract: one "ask" callback closes the card immediately.
func TestTelegramSingleSelectStillFinalizesOnTap(t *testing.T) {
	svc := testService(t, policy.Policy{Default: "ask"})
	go svc.TelegramPump()
	done := make(chan protocol.AskResult, 1)
	go func() {
		done <- svc.Ask(protocol.AskRequest{
			Question:   "Pick one",
			Options:    []string{"red", "green"},
			TTLSeconds: 30,
		}, 1000)
	}()
	rid := waitForAsk(t, svc, 3*time.Second)
	svc.Verdicts <- telegram.Verdict{RID: rid, Decision: "ask", By: "42", Idxs: []int{1}}
	select {
	case res := <-done:
		if res.Status != "selected" || res.Selection != "green" || res.SelectionIdx != 1 {
			t.Fatalf("single ask = %+v, want selected green idx 1", res)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("single ask did not resolve on tap")
	}
}

// waitForSelIdxs blocks until the pending record's accumulated toggle set
// matches want, so the concurrent pump can be observed deterministically.
func waitForSelIdxs(t *testing.T, svc Service, rid string, want []int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rec, err := svc.Store.Load(rid)
		if err == nil && reflect.DeepEqual(rec.SelectedIdxs, want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	rec, _ := svc.Store.Load(rid)
	t.Fatalf("selected idxs never reached %v (got %v)", want, rec.SelectedIdxs)
}

// TestToggleAskRepaintsMultiSelectKeyboard proves the real Telegram path:
// a toggle edits the inline keyboard in place with the checkbox state and
// the current Submit count, and finalizes nothing.
func TestToggleAskRepaintsMultiSelectKeyboard(t *testing.T) {
	var gotMarkup string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if strings.HasSuffix(r.URL.Path, "/editMessageReplyMarkup") {
			gotMarkup = r.Form.Get("reply_markup")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"ok":true,"result":{"message_id":7,"chat":{"id":1}}}`)
	}))
	defer ts.Close()

	dir := t.TempDir()
	svc := New(config.Conf{StateDir: dir, Timeout: 5, BotToken: "test-token"})
	svc.TG.BaseURL = ts.URL
	if st := svc.getState(); st != nil {
		st.tgClient.BaseURL = ts.URL
	}
	if err := svc.Store.Init(); err != nil {
		t.Fatal(err)
	}
	rid := "toggle-ask-1"
	if err := svc.Store.Save(protocol.PendingRecord{
		Kind: "ask", Question: "q", Options: []string{"red", "green", "blue"},
		MultiSelect: true, RecommendedIndex: 2,
		Expires: time.Now().Add(time.Minute).Unix(),
	}, rid); err != nil {
		t.Fatal(err)
	}
	svc.Store.AttachPager(rid, "123", 7)

	svc.toggleAsk(rid, 1)
	for _, want := range []string{"ask:toggle-ask-1:t:1", "ask:toggle-ask-1:s:1", "✅", "⬜", "⭐"} {
		if !strings.Contains(gotMarkup, want) {
			t.Fatalf("repainted markup missing %q:\n%s", want, gotMarkup)
		}
	}
	if sel := svc.Store.SelectionInfo(rid); sel != nil {
		t.Fatalf("toggle must not finalize a selection: %+v", sel)
	}
}

func waitForAsk(t *testing.T, svc Service, d time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if items := svc.List().Items; len(items) == 1 && items[0].Kind == "ask" {
			return items[0].ID
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("ask petition never appeared in the pending list")
	return ""
}
