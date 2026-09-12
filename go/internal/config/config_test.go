package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUserConfigPersistenceAndPermissions(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "2fado", "config.json")
	t.Setenv("TWOFADO_USER_CONFIG", cfgPath)

	cfg := UserConfig{
		BotToken:  "test-bot-token-123",
		ChatID:    "-1001234567890",
		Approvers: []string{"111111", "222222"},
	}

	if err := SaveUserConfig(cfg); err != nil {
		t.Fatalf("SaveUserConfig failed: %v", err)
	}

	// Verify file permissions are strictly 0600
	info, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	perm := info.Mode().Perm()
	if perm != 0o600 {
		t.Fatalf("expected permissions 0600, got %04o", perm)
	}

	// Verify loading
	loaded, err := LoadUserConfig()
	if err != nil {
		t.Fatalf("LoadUserConfig failed: %v", err)
	}
	if loaded.BotToken != cfg.BotToken {
		t.Errorf("expected BotToken %q, got %q", cfg.BotToken, loaded.BotToken)
	}
	if loaded.ChatID != cfg.ChatID {
		t.Errorf("expected ChatID %q, got %q", cfg.ChatID, loaded.ChatID)
	}
	if len(loaded.Approvers) != 2 || loaded.Approvers[0] != "111111" || loaded.Approvers[1] != "222222" {
		t.Errorf("unexpected Approvers: %+v", loaded.Approvers)
	}
}

func TestLoadIntegratesUserConfig(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")
	t.Setenv("TWOFADO_USER_CONFIG", cfgPath)

	userCfg := UserConfig{
		BotToken:  "user-token-abc",
		ChatID:    "98765",
		Approvers: []string{"42"},
	}
	if err := SaveUserConfig(userCfg); err != nil {
		t.Fatal(err)
	}

	// Create a conf file with placeholders
	confFile := filepath.Join(tmpDir, "2fado.conf")
	confContent := `
BOT_TOKEN=__TELEGRAM_BOT_TOKEN__
APPROVERS=__TELEGRAM_USER_ID__
SOCKET=/tmp/test.sock
STATE_DIR=/tmp/test-state
`
	if err := os.WriteFile(confFile, []byte(confContent), 0o644); err != nil {
		t.Fatal(err)
	}

	c := Load(confFile)
	if c.BotToken != "user-token-abc" {
		t.Errorf("expected user config BotToken to override placeholder, got %q", c.BotToken)
	}
	if c.ChatID != "98765" {
		t.Errorf("expected ChatID %q, got %q", "98765", c.ChatID)
	}
	if len(c.Approvers) != 1 || c.Approvers[0] != "42" {
		t.Errorf("expected Approvers [42], got %+v", c.Approvers)
	}

	// Environment variable must override UserConfig
	t.Setenv("TWOFADO_BOT_TOKEN", "env-token-xyz")
	c2 := Load(confFile)
	if c2.BotToken != "env-token-xyz" {
		t.Errorf("expected env var to override user config, got %q", c2.BotToken)
	}
}

func TestGetEnvWithFallback(t *testing.T) {
	t.Setenv("TWOFADO_TEST_KEY", "twofado-val")
	t.Setenv("FADO_TEST_KEY", "fado-val")
	t.Setenv("LEGACY_KEY", "legacy-val")

	// Preferred wins when all are present
	if got := GetEnvWithFallback("TWOFADO_TEST_KEY", "FADO_TEST_KEY", "LEGACY_KEY"); got != "twofado-val" {
		t.Errorf("expected twofado-val, got %q", got)
	}

	// First fallback wins when preferred is absent
	os.Unsetenv("TWOFADO_TEST_KEY")
	if got := GetEnvWithFallback("TWOFADO_TEST_KEY", "FADO_TEST_KEY", "LEGACY_KEY"); got != "fado-val" {
		t.Errorf("expected fado-val, got %q", got)
	}

	// Second fallback wins when first two are absent
	os.Unsetenv("FADO_TEST_KEY")
	if got := GetEnvWithFallback("TWOFADO_TEST_KEY", "FADO_TEST_KEY", "LEGACY_KEY"); got != "legacy-val" {
		t.Errorf("expected legacy-val, got %q", got)
	}

	// Empty string when none are set
	os.Unsetenv("LEGACY_KEY")
	if got := GetEnvWithFallback("TWOFADO_TEST_KEY", "FADO_TEST_KEY", "LEGACY_KEY"); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestConfigEnvResolutionPrecedence(t *testing.T) {
	tmpDir := t.TempDir()
	confFile := filepath.Join(tmpDir, "dummy.conf")
	if err := os.WriteFile(confFile, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	// 1. Socket: TWOFADO_SOCKET over FADO_SOCKET
	t.Setenv("TWOFADO_SOCKET", "/tmp/twofado.sock")
	t.Setenv("FADO_SOCKET", "/tmp/fado.sock")
	c := Load(confFile)
	if c.Socket != "/tmp/twofado.sock" {
		t.Errorf("expected Socket %q, got %q", "/tmp/twofado.sock", c.Socket)
	}
	os.Unsetenv("TWOFADO_SOCKET")
	c = Load(confFile)
	if c.Socket != "/tmp/fado.sock" {
		t.Errorf("expected fallback Socket %q, got %q", "/tmp/fado.sock", c.Socket)
	}
	os.Unsetenv("FADO_SOCKET")

	// 2. StateDir: TWOFADO_STATE_DIR over FADO_STATE_DIR
	t.Setenv("TWOFADO_STATE_DIR", "/tmp/twofado-state")
	t.Setenv("FADO_STATE_DIR", "/tmp/fado-state")
	c = Load(confFile)
	if c.StateDir != "/tmp/twofado-state" {
		t.Errorf("expected StateDir %q, got %q", "/tmp/twofado-state", c.StateDir)
	}
	os.Unsetenv("TWOFADO_STATE_DIR")
	c = Load(confFile)
	if c.StateDir != "/tmp/fado-state" {
		t.Errorf("expected fallback StateDir %q, got %q", "/tmp/fado-state", c.StateDir)
	}
	os.Unsetenv("FADO_STATE_DIR")

	// 3. Policy: TWOFADO_POLICY over FADO_POLICY
	t.Setenv("TWOFADO_POLICY", "/etc/twofado/policy.json")
	t.Setenv("FADO_POLICY", "/etc/fado/policy.json")
	c = Load(confFile)
	if c.Policy != "/etc/twofado/policy.json" {
		t.Errorf("expected Policy %q, got %q", "/etc/twofado/policy.json", c.Policy)
	}
	os.Unsetenv("TWOFADO_POLICY")
	c = Load(confFile)
	if c.Policy != "/etc/fado/policy.json" {
		t.Errorf("expected fallback Policy %q, got %q", "/etc/fado/policy.json", c.Policy)
	}
	os.Unsetenv("FADO_POLICY")

	// 4. ConfirmAll: TWOFADO_CONFIRM_ALL over FADO_CONFIRM_ALL
	t.Setenv("TWOFADO_CONFIRM_ALL", "true")
	t.Setenv("FADO_CONFIRM_ALL", "false")
	c = Load(confFile)
	if !c.ConfirmAll {
		t.Errorf("expected ConfirmAll true from TWOFADO_CONFIRM_ALL")
	}
	os.Unsetenv("TWOFADO_CONFIRM_ALL")
	t.Setenv("FADO_CONFIRM_ALL", "true")
	c = Load(confFile)
	if !c.ConfirmAll {
		t.Errorf("expected ConfirmAll true from FADO_CONFIRM_ALL")
	}
	os.Unsetenv("FADO_CONFIRM_ALL")

	// 5. BotToken: TWOFADO_BOT_TOKEN over FADO_BOT_TOKEN over BOT_TOKEN
	t.Setenv("TWOFADO_BOT_TOKEN", "tok-twofado")
	t.Setenv("FADO_BOT_TOKEN", "tok-fado")
	t.Setenv("BOT_TOKEN", "tok-raw")
	c = Load(confFile)
	if c.BotToken != "tok-twofado" {
		t.Errorf("expected BotToken %q, got %q", "tok-twofado", c.BotToken)
	}
	os.Unsetenv("TWOFADO_BOT_TOKEN")
	c = Load(confFile)
	if c.BotToken != "tok-fado" {
		t.Errorf("expected BotToken %q, got %q", "tok-fado", c.BotToken)
	}
	os.Unsetenv("FADO_BOT_TOKEN")
	c = Load(confFile)
	if c.BotToken != "tok-raw" {
		t.Errorf("expected BotToken %q, got %q", "tok-raw", c.BotToken)
	}
	os.Unsetenv("BOT_TOKEN")

	// 6. ChatID: TWOFADO_CHAT_ID over FADO_CHAT_ID over CHAT_ID
	t.Setenv("TWOFADO_CHAT_ID", "chat-twofado")
	t.Setenv("FADO_CHAT_ID", "chat-fado")
	t.Setenv("CHAT_ID", "chat-raw")
	c = Load(confFile)
	if c.ChatID != "chat-twofado" {
		t.Errorf("expected ChatID %q, got %q", "chat-twofado", c.ChatID)
	}
	os.Unsetenv("TWOFADO_CHAT_ID")
	c = Load(confFile)
	if c.ChatID != "chat-fado" {
		t.Errorf("expected ChatID %q, got %q", "chat-fado", c.ChatID)
	}
	os.Unsetenv("FADO_CHAT_ID")
	c = Load(confFile)
	if c.ChatID != "chat-raw" {
		t.Errorf("expected ChatID %q, got %q", "chat-raw", c.ChatID)
	}
	os.Unsetenv("CHAT_ID")

	// 7. Approvers: TWOFADO_APPROVERS over FADO_APPROVERS over APPROVERS
	t.Setenv("TWOFADO_APPROVERS", "app1")
	t.Setenv("FADO_APPROVERS", "app2")
	t.Setenv("APPROVERS", "app3")
	c = Load(confFile)
	if len(c.Approvers) != 1 || c.Approvers[0] != "app1" {
		t.Errorf("expected Approvers [app1], got %+v", c.Approvers)
	}
	os.Unsetenv("TWOFADO_APPROVERS")
	c = Load(confFile)
	if len(c.Approvers) != 1 || c.Approvers[0] != "app2" {
		t.Errorf("expected Approvers [app2], got %+v", c.Approvers)
	}
	os.Unsetenv("FADO_APPROVERS")
	c = Load(confFile)
	if len(c.Approvers) != 1 || c.Approvers[0] != "app3" {
		t.Errorf("expected Approvers [app3], got %+v", c.Approvers)
	}
	os.Unsetenv("APPROVERS")

	// 8. Pager: TWOFADO_PAGER over FADO_PAGER
	t.Setenv("TWOFADO_PAGER", "stdout")
	t.Setenv("FADO_PAGER", "custom")
	c = Load(confFile)
	if c.Pager != "stdout" {
		t.Errorf("expected Pager stdout, got %q", c.Pager)
	}
	os.Unsetenv("TWOFADO_PAGER")
	c = Load(confFile)
	if c.Pager != "custom" {
		t.Errorf("expected Pager custom, got %q", c.Pager)
	}
	os.Unsetenv("FADO_PAGER")

	// 9. UserConfigPath: TWOFADO_USER_CONFIG over FADO_USER_CONFIG
	t.Setenv("TWOFADO_USER_CONFIG", "/path/to/twofado.json")
	t.Setenv("FADO_USER_CONFIG", "/path/to/fado.json")
	if p := UserConfigPath(); p != "/path/to/twofado.json" {
		t.Errorf("expected /path/to/twofado.json, got %q", p)
	}
	os.Unsetenv("TWOFADO_USER_CONFIG")
	if p := UserConfigPath(); p != "/path/to/fado.json" {
		t.Errorf("expected /path/to/fado.json, got %q", p)
	}
	os.Unsetenv("FADO_USER_CONFIG")
}

