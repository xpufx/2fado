// Package config loads daemon configuration: file first, TWOFADO_ env
// overrides second (legacy FADO_ fallback). Placeholders (BOT_TOKEN starting with "__") select
// the stdout pager instead of Telegram.
package config

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type Conf struct {
	BotToken           string
	Approvers          []string
	ChatID             string
	Socket             string
	StateDir           string
	Policy             string
	Timeout            int
	DryRun             bool
	ConfirmAll         bool
	Pager              string
	NotificationTarget string
	AllowRootExec      bool
}

type UserConfig struct {
	BotToken           string   `json:"bot_token,omitempty"`
	ChatID             string   `json:"chat_id,omitempty"`
	Approvers          []string `json:"approvers,omitempty"`
	NotificationTarget string   `json:"notification_target,omitempty"`
}

// NormalizeNotificationTarget folds a raw target value onto the
// supported set (telegram|paseo|both), defaulting to both.
func NormalizeNotificationTarget(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "telegram", "paseo", "both":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return "both"
	}
}

// GetEnvWithFallback returns the value of the first non-empty environment variable
// among preferred and fallbacks (in order), or empty string if none are set.
func GetEnvWithFallback(preferred string, fallbacks ...string) string {
	if v := os.Getenv(preferred); v != "" {
		return v
	}
	for _, fb := range fallbacks {
		if v := os.Getenv(fb); v != "" {
			return v
		}
	}
	return ""
}

func UserConfigPath() string {
	if p := GetEnvWithFallback("TWOFADO_USER_CONFIG", "FADO_USER_CONFIG"); p != "" {
		return p
	}
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "2fado", "config.json")
}

func LoadUserConfig() (UserConfig, error) {
	p := UserConfigPath()
	data, err := os.ReadFile(p)
	if err != nil {
		return UserConfig{}, err
	}
	var cfg UserConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return UserConfig{}, err
	}
	return cfg, nil
}

func SaveUserConfig(cfg UserConfig) error {
	p := UserConfigPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(p, data, 0o600); err != nil {
		return err
	}
	return os.Chmod(p, 0o600)
}

// ParseAllowRoot folds a raw ALLOW_ROOT value onto bool, fail-closed:
// only 1/true/yes (case-insensitive, trimmed) enable; everything else,
// including unreadable/ambiguous values, yields false.
func ParseAllowRoot(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

func Load(path string) Conf {
	kv := map[string]string{}
	if f, err := os.Open(path); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			k, v, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			kv[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), "\"")
		}
		f.Close()
	}

	userCfg, _ := LoadUserConfig()

	get := func(k string, def string, extraFallbacks ...string) string {
		fallbacks := append([]string{"FADO_" + k}, extraFallbacks...)
		if v := GetEnvWithFallback("TWOFADO_"+k, fallbacks...); v != "" {
			return v
		}
		if v, ok := kv[k]; ok {
			return v
		}
		return def
	}

	botToken := get("BOT_TOKEN", "", "BOT_TOKEN")
	if (botToken == "" || strings.HasPrefix(botToken, "__")) && userCfg.BotToken != "" {
		botToken = userCfg.BotToken
	}

	chatID := get("CHAT_ID", "", "CHAT_ID")
	if chatID == "" && userCfg.ChatID != "" {
		chatID = userCfg.ChatID
	}

	c := Conf{
		BotToken: botToken,
		ChatID:   chatID,
		Socket:   get("SOCKET", "/tmp/2fado.sock"),
		StateDir: get("STATE_DIR", "/tmp/2fado-state"),
		Policy:   get("POLICY", "/etc/2fado/policy.json"),
		Timeout:  300,
		Pager:    get("PAGER", ""),
	}
	target := get("NOTIFICATION_TARGET", "")
	if target == "" {
		target = userCfg.NotificationTarget
	}
	c.NotificationTarget = NormalizeNotificationTarget(target)
	approversStr := get("APPROVERS", "", "APPROVERS")
	for _, a := range strings.Split(approversStr, ",") {
		if a = strings.TrimSpace(a); a != "" && !strings.HasPrefix(a, "__") {
			c.Approvers = append(c.Approvers, a)
		}
	}
	if len(c.Approvers) == 0 && len(userCfg.Approvers) > 0 {
		c.Approvers = userCfg.Approvers
	}
	c.DryRun = get("DRY_RUN", "") == "true"
	c.ConfirmAll = get("CONFIRM_ALL", "") == "true"
	// #54 Path A: ALLOW_ROOT is release-hidden; never honored. The key is
	// still read nowhere effective so no runtime surface can enable
	// escalation. ParseAllowRoot stays in-tree for a later release.
	c.AllowRootExec = false
	_ = get("ALLOW_ROOT", "")
	if t := get("TIMEOUT", ""); t != "" {
		var n int
		for _, ch := range t {
			n = n*10 + int(ch-'0')
		}
		if n > 0 {
			c.Timeout = n
		}
	}
	return c
}

func (c Conf) ApproverSet() map[string]bool {
	set := map[string]bool{}
	for _, a := range c.Approvers {
		set[a] = true
	}
	return set
}

func (c Conf) Placeholder() bool {
	return c.Pager == "stdout" || c.BotToken == "" || strings.HasPrefix(c.BotToken, "__")
}
