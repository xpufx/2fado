// Package config loads daemon configuration: file first, FADO_ env
// overrides second. Placeholders (BOT_TOKEN starting with "__") select
// the stdout pager instead of Telegram.
package config

import (
	"bufio"
	"os"
	"strings"
)

type Conf struct {
	BotToken   string
	Approvers  []string
	ChatID     string
	Socket     string
	StateDir   string
	Policy     string
	Timeout    int
	DryRun     bool
	ConfirmAll bool
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
	get := func(k, def string) string {
		if v, ok := os.LookupEnv("FADO_" + k); ok {
			return v
		}
		if v, ok := kv[k]; ok {
			return v
		}
		return def
	}
	c := Conf{
		BotToken: get("BOT_TOKEN", ""),
		ChatID:   get("CHAT_ID", ""),
		Socket:   get("SOCKET", "/tmp/2fado.sock"),
		StateDir: get("STATE_DIR", "/tmp/2fado-state"),
		Policy:   get("POLICY", "/etc/2fado/policy.json"),
		Timeout:  300,
	}
	for _, a := range strings.Split(get("APPROVERS", ""), ",") {
		if a = strings.TrimSpace(a); a != "" {
			c.Approvers = append(c.Approvers, a)
		}
	}
	c.DryRun = get("DRY_RUN", "") == "true"
	c.ConfirmAll = get("CONFIRM_ALL", "") == "true"
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
	return c.BotToken == "" || strings.HasPrefix(c.BotToken, "__")
}
