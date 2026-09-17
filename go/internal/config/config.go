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
	// ExecTimeoutSecs bounds wall-clock execution of spawned commands
	// (TWOFADO_EXEC_TIMEOUT, seconds, default 120, 0 disables).
	ExecTimeoutSecs int
	// MaxOutputBytes caps captured stdout+stderr per execution
	// (TWOFADO_MAX_OUTPUT_BYTES, bytes, default 262144). Excess is cut
	// with an explicit truncation marker.
	MaxOutputBytes int
	// MaxTelegramBodyBytes caps a single Telegram Bot API response body
	// read (TWOFADO_MAX_TELEGRAM_BODY_BYTES, bytes, default 1048576).
	MaxTelegramBodyBytes int
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
		Socket:   get("SOCKET", DefaultSocketPath()),
		StateDir: get("STATE_DIR", DefaultStateDir()),
		Policy:   get("POLICY", "/etc/2fado/policy.json"),
		Timeout:  300,
		Pager:    get("PAGER", ""),
		// #60: execution timeout + output ceilings. EXEC_TIMEOUT seconds
		// (default 120); MAX_OUTPUT_BYTES (default 256 KiB);
		// MAX_TELEGRAM_BODY_BYTES (default 1 MiB). Non-positive or
		// unparsable values fall back to defaults.
		ExecTimeoutSecs:      120,
		MaxOutputBytes:       262144,
		MaxTelegramBodyBytes: 1048576,
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
	if n := parsePositiveInt(get("EXEC_TIMEOUT", "")); n > 0 {
		c.ExecTimeoutSecs = n
	}
	if n := parsePositiveInt(get("MAX_OUTPUT_BYTES", "")); n > 0 {
		c.MaxOutputBytes = n
	}
	if n := parsePositiveInt(get("MAX_TELEGRAM_BODY_BYTES", "")); n > 0 {
		c.MaxTelegramBodyBytes = n
	}
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

// parsePositiveInt parses a decimal int, returning <=0 on any invalid input.
func parsePositiveInt(raw string) int {
	n := 0
	if raw == "" {
		return 0
	}
	for _, ch := range raw {
		if ch < '0' || ch > '9' {
			return 0
		}
		n = n*10 + int(ch-'0')
	}
	return n
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
