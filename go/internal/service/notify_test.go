package service

import (
	"os"
	"strings"
	"testing"
	"time"

	"2fado/internal/policy"
	"2fado/internal/protocol"
	"2fado/internal/telegram"
)

func TestNotifyCreatesPendingNoExec(t *testing.T) {
	svc := testService(t, policy.Policy{Default: "ask"})
	res := svc.Notify(protocol.NotifyRequest{
		Link:    "https://www.npmjs.com/login?otp=webauthn&stage_id=abc",
		Summary: "pkg@1.2.3 staged, sha abc123",
	}, 1000)
	if res.Status != "pending" || res.ID == "" {
		t.Fatalf("Notify = %+v, want pending with ID", res)
	}
	st := svc.Status(res.ID)
	if st.Status != "pending" || st.Kind != "notify" {
		t.Fatalf("Status = %+v, want pending notify", st)
	}
	if st.Link != "https://www.npmjs.com/login?otp=webauthn&stage_id=abc" || st.Summary != "pkg@1.2.3 staged, sha abc123" {
		t.Fatalf("Status link/summary = %+v", st)
	}
	if st.Acked {
		t.Fatal("fresh notify must not be acked")
	}
	items := svc.List().Items
	if len(items) != 1 || items[0].Kind != "notify" || items[0].Link == "" || items[0].Summary == "" {
		t.Fatalf("List = %+v, want one notify item with link+summary", items)
	}
	if svc.Store.Verdict(res.ID) != nil {
		t.Fatal("notify must not create a verdict (no exec path)")
	}
}

func TestNotifyValidation(t *testing.T) {
	svc := testService(t, policy.Policy{Default: "ask"})
	for _, req := range []protocol.NotifyRequest{
		{Link: "", Summary: "x"},
		{Link: "https://example.com/x", Summary: ""},
		{Link: "ftp://example.com/x", Summary: "x"},
		{Link: "not-a-url", Summary: "x"},
	} {
		if res := svc.Notify(req, 1000); res.Status != "denied" {
			t.Errorf("Notify(%+v) = %+v, want denied", req, res)
		}
	}
	if n := len(svc.List().Items); n != 0 {
		t.Errorf("invalid notifies created %d records", n)
	}
}

func TestNotifyTTLBounds(t *testing.T) {
	svc := testService(t, policy.Policy{Default: "ask"})
	res := svc.Notify(protocol.NotifyRequest{
		Link: "https://example.com/a", Summary: "default ttl",
	}, 1000)
	rec, err := svc.Store.Load(res.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ttl := time.Until(time.Unix(rec.Expires, 0)); ttl < 23*time.Hour || ttl > 25*time.Hour {
		t.Errorf("default TTL = %v, want ~24h", ttl)
	}
	res2 := svc.Notify(protocol.NotifyRequest{
		Link: "https://example.com/b", Summary: "custom ttl", TTLSeconds: 3600,
	}, 1000)
	st := svc.Status(res2.ID)
	if st.ExpiresIn < 3500 || st.ExpiresIn > 3600 {
		t.Errorf("custom TTL expires_in = %d, want ~3600", st.ExpiresIn)
	}
	res3 := svc.Notify(protocol.NotifyRequest{
		Link: "https://example.com/c", Summary: "clamp min", TTLSeconds: 1,
	}, 1000)
	rec3, _ := svc.Store.Load(res3.ID)
	if ttl := time.Until(time.Unix(rec3.Expires, 0)); ttl < MinNotifyTTL-10*time.Second {
		t.Errorf("min clamp TTL = %v, want >= %v", ttl, MinNotifyTTL)
	}
}

func TestAckLifecycleVisibleInStatus(t *testing.T) {
	svc := testService(t, policy.Policy{Default: "ask"})
	op := uint32(os.Getuid())
	res := svc.Notify(protocol.NotifyRequest{
		Link: "https://example.com/stage", Summary: "needs human",
	}, 1000)
	ack := svc.Ack(protocol.AckSubmit{ID: res.ID, By: "paseo"}, op)
	if !ack.Acked || ack.Error != "" {
		t.Fatalf("Ack = %+v, want acked", ack)
	}
	dup := svc.Ack(protocol.AckSubmit{ID: res.ID, By: "paseo"}, op)
	if dup.Acked {
		t.Error("second ack must not win (first ack wins)")
	}
	st := svc.Status(res.ID)
	if st.Status != "acked" || !st.Acked || st.AckBy != "paseo" || st.Decision != "ack" {
		t.Fatalf("Status after ack = %+v, want acked with ack_by", st)
	}
	if st.Link == "" || st.Summary == "" {
		t.Errorf("acked status must keep link+summary: %+v", st)
	}
	if items := svc.List().Items; len(items) != 0 {
		t.Fatalf("acked notify must clear pending list, got %+v", items)
	}
	recent := svc.Recent(10).Items
	found := false
	for _, it := range recent {
		if it.ID == res.ID {
			found = true
			if it.Decision != "ack" || !it.Acked || it.Link == "" {
				t.Errorf("recent ack item = %+v", it)
			}
		}
	}
	if !found {
		t.Error("acked notify missing from recent history")
	}
}

func TestAckRejectsExecAndUnknown(t *testing.T) {
	svc := testService(t, policy.Policy{Default: "ask"})
	op := uint32(os.Getuid())
	rid := "exec-1"
	if err := svc.Store.Save(protocol.PendingRecord{
		Argv: []string{"/bin/echo"}, UID: 1000, Cwd: "/tmp",
		Expires: time.Now().Add(time.Hour).Unix(),
	}, rid); err != nil {
		t.Fatal(err)
	}
	if ack := svc.Ack(protocol.AckSubmit{ID: rid}, op); ack.Acked {
		t.Error("ack on exec petition must be rejected")
	}
	if ack := svc.Ack(protocol.AckSubmit{ID: "nope"}, op); ack.Acked {
		t.Error("ack on unknown id must be rejected")
	}
	if ack := svc.Ack(protocol.AckSubmit{ID: rid}, unprivilegedUID()); ack.Error != "unauthorized" {
		t.Errorf("unprivileged ack = %+v, want unauthorized", ack)
	}
}

func TestNotifyCardRendersLinkSummaryAndAck(t *testing.T) {
	card := telegram.NotifyCard("host1", "https://example.com/stage?id=1", "pkg@9.9.9 <b>bold</b>", "rid1", 3600, false, "")
	if !strings.Contains(card, "https://example.com/stage?id=1") {
		t.Errorf("card missing link:\n%s", card)
	}
	if !strings.Contains(card, "pkg@9.9.9") || strings.Contains(card, "<b>bold</b>") {
		t.Errorf("summary must be escaped:\n%s", card)
	}
	if !strings.Contains(card, "📣") {
		t.Errorf("pending notify card needs action header:\n%s", card)
	}
	acked := telegram.NotifyCard("host1", "https://example.com/stage", "s", "rid1", 0, true, "paseo")
	if !strings.Contains(acked, "Acknowledged via Paseo Desktop") {
		t.Errorf("acked card missing ack header:\n%s", acked)
	}
	btns := telegram.NotifyButtons("https://example.com/stage", "rid1")
	if !strings.Contains(btns, "ack:rid1") || !strings.Contains(btns, "https://example.com/stage") {
		t.Errorf("notify buttons need ack callback + link url:\n%s", btns)
	}
}

func TestNotifyExpiryAndPrune(t *testing.T) {
	svc := testService(t, policy.Policy{Default: "ask"})
	res := svc.Notify(protocol.NotifyRequest{
		Link: "https://example.com/exp", Summary: "short", TTLSeconds: int64((5 * time.Minute) / time.Second),
	}, 1000)
	rec, _ := svc.Store.Load(res.ID)
	rec.Expires = time.Now().Add(-time.Hour).Unix()
	_ = svc.Store.Save(rec, res.ID)
	if st := svc.Status(res.ID); st.Status != "timeout" {
		t.Fatalf("expired notify status = %q, want timeout", st.Status)
	}
	if items := svc.List().Items; len(items) != 0 {
		t.Fatalf("expired notify still listed: %+v", items)
	}
	ancient := time.Now().Add(-48 * time.Hour)
	for _, suf := range []string{".json", ".ack"} {
		p := svc.Store.Pending + "/" + res.ID + suf
		if suf == ".json" {
			_ = os.Chtimes(p, ancient, ancient)
			continue
		}
		_ = os.WriteFile(p, []byte("{}"), 0o600)
		_ = os.Chtimes(p, ancient, ancient)
	}
	if n, err := svc.Store.Prune(24 * time.Hour); err != nil || n != 1 {
		t.Fatalf("Prune = (%d, %v), want (1, nil)", n, err)
	}
}
