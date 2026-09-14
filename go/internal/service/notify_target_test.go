package service

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"2fado/internal/config"
	"2fado/internal/policy"
	"2fado/internal/protocol"
)

func TestNormalizeNotificationTarget(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{"telegram", "telegram"},
		{"paseo", "paseo"},
		{"both", "both"},
		{"Paseo", "paseo"},
		{" both ", "both"},
		{"", "both"},
		{"sms", "both"},
		{"fallback", "both"},
	} {
		if got := config.NormalizeNotificationTarget(tc.in); got != tc.want {
			t.Errorf("NormalizeNotificationTarget(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNotifyTargetDefaultsToBoth(t *testing.T) {
	svc := testService(t, policy.Policy{Default: "ask"})
	if got := svc.notifyTarget(); got != "both" {
		t.Fatalf("notifyTarget() = %q, want both", got)
	}
	if !svc.notifyTelegram() || !svc.notifyPaseo() {
		t.Fatal("default target must page both Telegram and Paseo")
	}
}

type targetServer struct {
	ts        *httptest.Server
	sends     int
	validUser string
}

func newTargetServer(t *testing.T) *targetServer {
	t.Helper()
	s := &targetServer{validUser: "target_bot"}
	s.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			fmt.Fprintln(w, `{"ok":true,"result":{"id":999,"is_bot":true,"username":"target_bot"}}`)
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			s.sends++
			fmt.Fprintln(w, `{"ok":true,"result":{"message_id":7,"chat":{"id":1}}}`)
		case strings.HasSuffix(r.URL.Path, "/editMessageText"):
			fmt.Fprintln(w, `{"ok":true,"result":{"message_id":7,"chat":{"id":1}}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.ts.Close)
	return s
}

func newTargetService(t *testing.T, s *targetServer, target string) Service {
	t.Helper()
	dir := t.TempDir()
	svc := New(config.Conf{StateDir: dir, Timeout: 5, BotToken: "test-token", NotificationTarget: target})
	svc.TG.BaseURL = s.ts.URL
	if svc.getState() != nil {
		svc.getState().tgClient.BaseURL = s.ts.URL
	}
	if err := svc.Store.Init(); err != nil {
		t.Fatal(err)
	}
	return svc
}

func saveTargetRecord(t *testing.T, svc Service, rid string) {
	t.Helper()
	if err := svc.Store.Save(protocol.PendingRecord{
		Argv: []string{"/bin/echo", "hi"}, UID: 1000, Cwd: "/tmp",
		Expires: time.Now().Add(60 * time.Second).Unix(),
	}, rid); err != nil {
		t.Fatal(err)
	}
}

func TestPageSkipsTelegramWhenPaseoOnly(t *testing.T) {
	s := newTargetServer(t)
	svc := newTargetService(t, s, "paseo")
	if svc.notifyTelegram() {
		t.Fatal("paseo-only must not page Telegram")
	}
	if !svc.notifyPaseo() {
		t.Fatal("paseo-only must page Paseo")
	}
	saveTargetRecord(t, svc, "paseo-only-1")
	svc.page("paseo-only-1",
		protocol.RunRequest{Argv: []string{"/bin/echo", "hi"}, Cwd: "/tmp"}, 1000)
	if s.sends != 0 {
		t.Fatalf("paseo-only sent %d Telegram messages, want 0", s.sends)
	}
	rec, err := svc.Store.Load("paseo-only-1")
	if err != nil {
		t.Fatal(err)
	}
	if rec.ChatID != "" {
		t.Fatalf("paseo-only attached Telegram pager chat %q, want none", rec.ChatID)
	}
	if items := svc.List().Items; len(items) != 1 {
		t.Fatalf("paseo-only list = %d items, want 1", len(items))
	}
	svc.pageNotify("paseo-notify-1", "https://example.com/x", "needs human", 1000, time.Hour)
	if s.sends != 0 {
		t.Fatalf("paseo-only notify sent %d Telegram messages, want 0", s.sends)
	}
}

func TestPageSendsTelegramAndHidesListWhenTelegramOnly(t *testing.T) {
	s := newTargetServer(t)
	svc := newTargetService(t, s, "telegram")
	if !svc.notifyTelegram() {
		t.Fatal("telegram-only must page Telegram")
	}
	if svc.notifyPaseo() {
		t.Fatal("telegram-only must not page Paseo")
	}
	saveTargetRecord(t, svc, "telegram-only-1")
	svc.page("telegram-only-1",
		protocol.RunRequest{Argv: []string{"/bin/echo", "hi"}, Cwd: "/tmp"}, 1000)
	if s.sends != 1 {
		t.Fatalf("telegram-only sent %d Telegram messages, want 1", s.sends)
	}
	if items := svc.List().Items; len(items) != 0 {
		t.Fatalf("telegram-only list = %d items, want 0", len(items))
	}
}

func TestBothPagesEverywhere(t *testing.T) {
	s := newTargetServer(t)
	svc := newTargetService(t, s, "")
	saveTargetRecord(t, svc, "both-1")
	svc.page("both-1",
		protocol.RunRequest{Argv: []string{"/bin/echo", "hi"}, Cwd: "/tmp"}, 1000)
	if s.sends != 1 {
		t.Fatalf("default target sent %d Telegram messages, want 1", s.sends)
	}
	if items := svc.List().Items; len(items) != 1 {
		t.Fatalf("default target list = %d items, want 1", len(items))
	}
}

func TestTelegramSetConfigUpdatesTarget(t *testing.T) {
	s := newTargetServer(t)
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")
	t.Setenv("TWOFADO_USER_CONFIG", cfgPath)

	svc := New(config.Conf{StateDir: tmpDir, Timeout: 5})
	svc.TG.BaseURL = s.ts.URL
	if svc.state != nil {
		svc.state.tgClient.BaseURL = s.ts.URL
	}
	op := uint32(os.Getuid())
	ptr := func(v string) *string { return &v }

	bad := svc.TelegramSetConfig(protocol.TelegramSetConfigRequest{
		NotificationTarget: "sms",
	}, op)
	if bad.Success || !strings.Contains(bad.Error, "notification_target") {
		t.Fatalf("invalid target = %+v, want rejection", bad)
	}

	res := svc.TelegramSetConfig(protocol.TelegramSetConfigRequest{
		BotToken:           ptr("valid-token"),
		ChatID:             "123",
		Approvers:          []string{"111"},
		NotificationTarget: "paseo",
	}, op)
	if !res.Success {
		t.Fatalf("TelegramSetConfig failed: %s", res.Error)
	}
	if got := svc.notifyTarget(); got != "paseo" {
		t.Fatalf("notifyTarget() = %q, want paseo", got)
	}
	info := svc.TelegramInfo()
	if info.NotificationTarget != "paseo" {
		t.Fatalf("info target = %q, want paseo", info.NotificationTarget)
	}
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"notification_target": "paseo"`) {
		t.Fatalf("persisted user config missing target:\n%s", raw)
	}

	keep := svc.TelegramSetConfig(protocol.TelegramSetConfigRequest{}, op)
	if !keep.Success {
		t.Fatalf("empty update failed: %s", keep.Error)
	}
	if got := svc.notifyTarget(); got != "paseo" {
		t.Fatalf("empty update reset target to %q, want paseo kept", got)
	}

	if denied := svc.TelegramSetConfig(protocol.TelegramSetConfigRequest{
		NotificationTarget: "telegram",
	}, unprivilegedUID()); denied.Success || denied.Error != "unauthorized" {
		t.Fatalf("unprivileged target update = %+v, want unauthorized", denied)
	}
}
