package service

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"2fado/internal/config"
	"2fado/internal/policy"
	"2fado/internal/protocol"
)

func TestTelegramInfoUnconfigured(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("TWOFADO_USER_CONFIG", filepath.Join(tmpDir, "config.json"))

	svc := testService(t, policy.Policy{Default: "ask"})
	info := svc.TelegramInfo()

	if info.Configured {
		t.Error("expected Configured to be false")
	}
	if info.Status != "unconfigured" {
		t.Errorf("expected status 'unconfigured', got %q", info.Status)
	}
	if info.Approvers == nil {
		t.Error("expected Approvers slice to be non-nil")
	}
}

func TestTelegramSetConfigAndInfoLifecycle(t *testing.T) {
	var callCount int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if r.URL.Path == "/botvalid-token/getMe" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintln(w, `{"ok":true,"result":{"id":999,"is_bot":true,"username":"super_approval_bot"}}`)
			return
		}
		if r.URL.Path == "/botbad-token/getMe" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintln(w, `{"ok":false,"error_code":401,"description":"Unauthorized"}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")
	t.Setenv("TWOFADO_USER_CONFIG", cfgPath)

	cfg := config.Conf{
		StateDir: tmpDir,
		Timeout:  5,
	}
	svc := New(cfg)
	// Point mock base URL
	svc.TG.BaseURL = ts.URL
	if svc.state != nil {
		svc.state.tgClient.BaseURL = ts.URL
	}

	// 1. Initial info should be unconfigured
	info := svc.TelegramInfo()
	if info.Configured || info.Status != "unconfigured" {
		t.Fatalf("expected unconfigured initial state, got %+v", info)
	}

	ptr := func(s string) *string { return &s }

	// 2. Setting invalid token should fail and NOT overwrite config
	badRes := svc.TelegramSetConfig(protocol.TelegramSetConfigRequest{
		BotToken:  ptr("bad-token"),
		ChatID:    "123",
		Approvers: []string{"admin"},
	})
	if badRes.Success {
		t.Fatal("expected failure for bad token, got success")
	}
	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
		t.Fatalf("config file should not have been written on failure")
	}

	// 3. Setting valid token should succeed, persist file with 0600, and return bot username
	goodRes := svc.TelegramSetConfig(protocol.TelegramSetConfigRequest{
		BotToken:  ptr("valid-token"),
		ChatID:    "-100987654",
		Approvers: []string{"111", "222"},
	})
	if !goodRes.Success {
		t.Fatalf("TelegramSetConfig failed: %s", goodRes.Error)
	}
	if goodRes.BotUsername != "super_approval_bot" {
		t.Errorf("expected username 'super_approval_bot', got %q", goodRes.BotUsername)
	}

	// Verify file mode 0600
	fi, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("expected 0600 perms, got %04o", fi.Mode().Perm())
	}

	// 4. TelegramInfo should reflect configured state and cached bot username
	infoAfter := svc.TelegramInfo()
	if !infoAfter.Configured {
		t.Error("expected Configured = true")
	}
	if infoAfter.Status != "connected" {
		t.Errorf("expected Status = 'connected', got %q", infoAfter.Status)
	}
	if infoAfter.BotUsername != "super_approval_bot" {
		t.Errorf("expected BotUsername = 'super_approval_bot', got %q", infoAfter.BotUsername)
	}
	if infoAfter.ChatID != "-100987654" {
		t.Errorf("expected ChatID = '-100987654', got %q", infoAfter.ChatID)
	}
	if len(infoAfter.Approvers) != 2 || infoAfter.Approvers[0] != "111" || infoAfter.Approvers[1] != "222" {
		t.Errorf("unexpected Approvers: %+v", infoAfter.Approvers)
	}

	// 5. Unconfiguring (empty token)
	clearRes := svc.TelegramSetConfig(protocol.TelegramSetConfigRequest{
		BotToken:  ptr(""),
		ChatID:    "",
		Approvers: []string{},
	})
	if !clearRes.Success {
		t.Fatalf("failed to unconfigure: %s", clearRes.Error)
	}
	infoCleared := svc.TelegramInfo()
	if infoCleared.Configured {
		t.Error("expected Configured = false after clear")
	}
	if infoCleared.Status != "unconfigured" {
		t.Errorf("expected status 'unconfigured', got %q", infoCleared.Status)
	}
}
