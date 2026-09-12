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
