package service

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"2fado/internal/config"
	"2fado/internal/policy"
	"2fado/internal/protocol"
)

func TestExtractAuthURL(t *testing.T) {
	cases := []struct {
		name   string
		output string
		want   string
	}{
		{
			"npm webauthn ceremony",
			"Visit https://www.npmjs.com/login?otp=webauthn&stage_id=abc123 to publish",
			"https://www.npmjs.com/login?otp=webauthn&stage_id=abc123",
		},
		{
			"github device flow",
			"First copy your one-time code: ABCD-1234\nThen visit https://github.com/login/device to authenticate.",
			"https://github.com/login/device",
		},
		{
			"registry stage poll",
			"waiting on https://registry.npmjs.org/-/package/foo/stage?session=xyz",
			"https://registry.npmjs.org/-/package/foo/stage?session=xyz",
		},
		{
			"plain homepage is not a challenge",
			"see https://www.npmjs.com/package/foo for docs",
			"",
		},
		{
			"no url at all",
			"published successfully",
			"",
		},
	}
	for _, tc := range cases {
		if got := ExtractAuthURL(tc.output); got != tc.want {
			t.Errorf("%s: ExtractAuthURL = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestRunApprovedCapturesAuthURL(t *testing.T) {
	svc := testService(t, policy.Policy{Default: "ask"})
	rid := "auth-challenge-1"
	url := "https://www.npmjs.com/login?otp=webauthn&stage_id=test1"
	if err := svc.Store.Save(protocol.PendingRecord{
		Argv: []string{"/bin/echo", url}, UID: 1000, Cwd: t.TempDir(),
		Expires: time.Now().Add(60 * time.Second).Unix(),
	}, rid); err != nil {
		t.Fatal(err)
	}
	if !svc.Store.Consume(rid, "approve", "tester") {
		t.Fatal("failed to record approval")
	}
	res := svc.runApproved(rid, protocol.RunRequest{
		Argv: []string{"/bin/echo", url}, Cwd: t.TempDir(),
	}, 1000, map[string]string{"PATH": "/usr/bin:/bin"})
	if res.Status != "allowed" {
		t.Fatalf("runApproved = %+v, want allowed", res)
	}
	st := svc.Store.Status(rid, time.Now().Unix())
	if st.AuthURL != url {
		t.Fatalf("status AuthURL = %q, want %q", st.AuthURL, url)
	}
	recents := svc.Store.Recent(10)
	found := false
	for _, it := range recents {
		if it.ID == rid && it.AuthURL == url {
			found = true
		}
	}
	if !found {
		t.Fatalf("recent list missing AuthURL for %s: %+v", rid, recents)
	}
	items := svc.Store.List(time.Now().Unix())
	for _, it := range items {
		if it.ID == rid {
			t.Fatalf("decided request %s still pending", rid)
		}
	}
}

func TestNotifyAuthURLPostsTelegramButton(t *testing.T) {
	var gotText, gotMarkup, gotReply string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if strings.HasSuffix(r.URL.Path, "/sendMessage") {
			gotText = r.Form.Get("text")
			gotMarkup = r.Form.Get("reply_markup")
			gotReply = r.Form.Get("reply_to_message_id")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"ok":true,"result":{"message_id":9,"chat":{"id":1}}}`)
	}))
	defer ts.Close()

	dir := t.TempDir()
	svc := New(config.Conf{StateDir: dir, Timeout: 5, BotToken: "test-token"})
	svc.TG.BaseURL = ts.URL
	if svc.getState() != nil {
		svc.getState().tgClient.BaseURL = ts.URL
	}
	if err := svc.Store.Init(); err != nil {
		t.Fatal(err)
	}
	rid := "auth-notify-1"
	url := "https://www.npmjs.com/login?otp=webauthn&stage_id=test2"
	if err := svc.Store.Save(protocol.PendingRecord{
		Argv: []string{"npm", "publish"}, UID: 1000, Cwd: "/tmp",
		Expires: time.Now().Add(60 * time.Second).Unix(),
	}, rid); err != nil {
		t.Fatal(err)
	}
	svc.Store.AttachPager(rid, "123", 7)

	svc.notifyAuthURL(rid, protocol.RunRequest{Argv: []string{"npm", "publish"}}, url)
	if !strings.Contains(gotText, "Biometric 2FA required") {
		t.Errorf("challenge message missing, got:\n%s", gotText)
	}
	if !strings.Contains(gotMarkup, "Open WebAuthn Verification") || !strings.Contains(gotMarkup, "npmjs.com/login") {
		t.Errorf("auth button missing or wrong URL, got:\n%s", gotMarkup)
	}
	if gotReply != "7" {
		t.Errorf("challenge not threaded to card, reply_to=%q", gotReply)
	}
}
