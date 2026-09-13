package policy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTierWhitelistWinsOverBlacklist(t *testing.T) {
	p := Policy{
		Whitelist: [][]string{{"/bin/echo", "hi"}},
		Blacklist: [][]string{{"/bin/echo", "hi"}},
		Default:   "ask",
	}
	if got := p.Tier([]string{"/bin/echo", "hi"}); got != "allow" {
		t.Fatalf("Tier = %q, want allow", got)
	}
}

func TestNeedsConfirm(t *testing.T) {
	p := Policy{
		Confirm: [][]string{
			{"/bin/echo", "dangerous"},
			{"rm", "-rf", "/"},
		},
	}
	if !p.NeedsConfirm([]string{"/bin/echo", "dangerous"}) {
		t.Errorf("expected NeedsConfirm for [/bin/echo dangerous]")
	}
	if !p.NeedsConfirm([]string{"rm", "-rf", "/"}) {
		t.Errorf("expected NeedsConfirm for [rm -rf /]")
	}
	if p.NeedsConfirm([]string{"/bin/echo", "safe"}) {
		t.Errorf("unexpected NeedsConfirm for [/bin/echo safe]")
	}
}

func TestWhitelistModeIgnoresBlacklistAndDefault(t *testing.T) {
	p := Policy{
		Mode:      "whitelist",
		Whitelist: [][]string{{"/bin/echo", "hi"}},
		Blacklist: [][]string{{"/bin/echo", "bye"}},
		Default:   "ask",
	}
	if got := p.Tier([]string{"/bin/echo", "hi"}); got != "allow" {
		t.Errorf("Tier = %q, want allow", got)
	}
	for _, argv := range [][]string{{"/bin/echo", "bye"}, {"/bin/ls"}} {
		if got := p.Tier(argv); got != "deny" {
			t.Errorf("Tier(%q) = %q, want deny (whitelist mode: blacklist+default are noop)", argv, got)
		}
	}
}

func TestTierExactMatchOnly(t *testing.T) {
	p := Policy{
		Whitelist: [][]string{{"/usr/bin/git", "status"}},
		Blacklist: [][]string{{"/bin/rm", "-rf", "/"}},
		Default:   "ask",
	}
	cases := []struct {
		name string
		argv []string
		want string
	}{
		{"allow exact", []string{"/usr/bin/git", "status"}, "allow"},
		{"deny exact", []string{"/bin/rm", "-rf", "/"}, "deny"},
		{"extra arg misses", []string{"/usr/bin/git", "status", "--short"}, "ask"},
		{"missing arg misses", []string{"/usr/bin/git"}, "ask"},
		{"order matters", []string{"status", "/usr/bin/git"}, "ask"},
		{"case sensitive", []string{"/usr/bin/GIT", "status"}, "ask"},
		{"joined shell string misses", []string{"/usr/bin/git status"}, "ask"},
		{"empty argv falls to default", nil, "ask"},
		{"unlisted falls to default", []string{"/bin/ls"}, "ask"},
	}
	for _, tc := range cases {
		if got := p.Tier(tc.argv); got != tc.want {
			t.Errorf("%s: Tier(%q) = %q, want %q", tc.name, tc.argv, got, tc.want)
		}
	}
}

func TestTierAdversarialMisses(t *testing.T) {
	p := Policy{
		Whitelist: [][]string{{"/bin/echo", "hi"}},
		Blacklist: [][]string{{"/bin/rm", "-rf", "/"}},
		Default:   "ask",
	}
	cases := []struct {
		name string
		argv []string
	}{
		{"semicolon chain", []string{"/bin/echo", "hi; rm -rf /"}},
		{"semicolon extra arg", []string{"/bin/echo", "hi", ";", "rm", "-rf", "/"}},
		{"ampersand chain", []string{"/bin/echo", "hi && rm -rf /"}},
		{"pipe chain", []string{"/bin/echo", "hi | rm -rf /"}},
		{"command substitution", []string{"/bin/echo", "hi$(rm -rf /)"}},
		{"backtick substitution", []string{"/bin/echo", "hi`rm -rf /`"}},
		{"newline injection", []string{"/bin/echo", "hi\nrm -rf /"}},
		{"carriage return overwrite", []string{"/bin/echo", "hi\rMALICIOUS"}},
		{"ansi hide", []string{"/bin/echo", "hi\x1b[8mMALICIOUS\x1b[28m"}},
		{"ansi erase line", []string{"/bin/echo", "hi\x1b[2K"}},
		{"fence break", []string{"/bin/echo", "hi\n```\nMALICIOUS"}},
		{"nul byte suffix", []string{"/bin/echo", "hi\x00rm -rf /"}},
		{"zero width joiner", []string{"/bin/echo", "hi⁠"}},
		{"zero width space", []string{"/bin/echo", "hi​"}},
		{"rtl override", []string{"/bin/echo", "hi‮"}},
		{"homoglyph path", []string{"/bіn/echo", "hi"}},
		{"fullwidth chars", []string{"/bin/echo", "ｈｉ"}},
		{"double slash path", []string{"/bin//echo", "hi"}},
		{"dot path", []string{"/bin/./echo", "hi"}},
		{"dotdot path", []string{"/bin/../bin/echo", "hi"}},
		{"trailing space", []string{"/bin/echo ", "hi"}},
		{"dashdash terminator", []string{"/bin/echo", "hi", "--"}},
	}
	for _, tc := range cases {
		if got := p.Tier(tc.argv); got != "ask" {
			t.Errorf("%s: Tier(%q) = %q, want ask (must miss allow, need explicit entry or human)", tc.name, tc.argv, got)
		}
	}
	if got := p.Tier([]string{"/bin/rm", "-rf", "/; echo hi"}); got != "ask" {
		t.Errorf("deny entry is exact too: Tier = %q, want ask", got)
	}
}

func TestTierDefaultPassthrough(t *testing.T) {
	for _, def := range []string{"ask", "deny"} {
		p := Policy{Default: def}
		if got := p.Tier([]string{"/bin/ls"}); got != def {
			t.Errorf("Default %q: Tier = %q, want %q", def, got, def)
		}
	}
}

func TestLoad(t *testing.T) {
	t.Run("missing file defaults to ask", func(t *testing.T) {
		p := Load(filepath.Join(t.TempDir(), "nope.json"))
		if p.Default != "ask" {
			t.Fatalf("Default = %q, want ask", p.Default)
		}
		if got := p.Tier([]string{"/bin/ls"}); got != "ask" {
			t.Fatalf("Tier = %q, want ask", got)
		}
	})

	t.Run("empty default normalizes to ask", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "policy.json")
		if err := os.WriteFile(path, []byte(`{"whitelist":[],"blacklist":[]}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := Load(path).Default; got != "ask" {
			t.Fatalf("Default = %q, want ask", got)
		}
	})

	t.Run("loads argv vectors", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "policy.json")
		body := `{"mode":"blacklist","whitelist":[["/bin/echo","hi"]],"blacklist":[["/bin/rm","-rf","/"]],"default":"ask"}`
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		p := Load(path)
		if got := p.Tier([]string{"/bin/echo", "hi"}); got != "allow" {
			t.Errorf("allow vector: Tier = %q, want allow", got)
		}
		if got := p.Tier([]string{"/bin/rm", "-rf", "/"}); got != "deny" {
			t.Errorf("deny vector: Tier = %q, want deny", got)
		}
		if got := p.Tier([]string{"/bin/echo hi"}); got != "ask" {
			t.Errorf("joined string must miss: Tier = %q, want ask", got)
		}
	})

	t.Run("malformed file fails closed to ask", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "policy.json")
		if err := os.WriteFile(path, []byte(`{"whitelist": "not-a-vector"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		p := Load(path)
		if p.Default != "ask" {
			t.Fatalf("Default = %q, want ask", p.Default)
		}
		if got := p.Tier([]string{"/bin/echo", "hi"}); got != "ask" {
			t.Fatalf("Tier = %q, want ask", got)
		}
	})
}

func TestAddRuleExactBaseCustom(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(path, []byte(`{"mode":"blacklist","default":"ask"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := AddRule(path, "whitelist", "exact", []string{"/bin/echo", "hi"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := AddRule(path, "whitelist", "base", []string{"/usr/bin/git"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := AddRule(path, "blacklist", "custom", []string{"/bin/rm", "*"}); err != nil {
		t.Fatal(err)
	}
	p := Load(path)
	if got := p.Tier([]string{"/bin/echo", "hi"}); got != "allow" {
		t.Errorf("exact allow: Tier = %q, want allow", got)
	}
	if got := p.Tier([]string{"/usr/bin/git", "status", "--short"}); got != "allow" {
		t.Errorf("base allow: Tier = %q, want allow", got)
	}
	if got := p.Tier([]string{"/bin/rm", "-rf", "/"}); got != "deny" {
		t.Errorf("custom deny: Tier = %q, want deny", got)
	}
	if got := p.Tier([]string{"/bin/ls"}); got != "ask" {
		t.Errorf("unlisted: Tier = %q, want ask", got)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("policy.json mode = %o, want 600", fi.Mode().Perm())
	}
	if _, _, err := AddRule(path, "nope", "exact", []string{"/bin/x"}); err == nil {
		t.Error("bad target must fail")
	}
	if _, _, err := AddRule(path, "whitelist", "bogus", []string{"/bin/x"}); err == nil {
		t.Error("bad match_type must fail")
	}
}

func TestTierBaseBinaryCanonicalization(t *testing.T) {
	p := Policy{
		Blacklist: [][]string{{"/bin/rm"}},
		Default:   "ask",
	}
	for _, argv := range [][]string{
		{"/bin/rm", "-rf", "/"},
		{"rm", "-rf", "/"},
		{"./rm", "-rf", "/"},
		{"/bin//rm", "-rf", "/"},
	} {
		if got := p.Tier(argv); got != "deny" {
			t.Errorf("Tier(%q) = %q, want deny (canonical base match)", argv, got)
		}
	}
	allow := Policy{
		Whitelist: [][]string{{"git"}},
		Default:   "ask",
	}
	for _, argv := range [][]string{
		{"git", "status"},
		{"/usr/bin/git", "status"},
	} {
		if got := allow.Tier(argv); got != "allow" {
			t.Errorf("Tier(%q) = %q, want allow (bare-name rule matches path)", argv, got)
		}
	}
	if got := allow.Tier([]string{"mygit", "status"}); got != "ask" {
		t.Errorf("Tier(mygit) = %q, want ask (no substring match)", got)
	}
}

func TestTierRestrictedInterpretersNeedArgs(t *testing.T) {
	p := Policy{
		Whitelist: [][]string{{"python3"}, {"/bin/bash"}, {"curl"}},
		Default:   "ask",
	}
	for _, argv := range [][]string{
		{"python3", "-c", "evil()"},
		{"/usr/bin/python3", "script.py"},
		{"bash", "-c", "rm -rf /"},
		{"curl", "@/etc/shadow"},
	} {
		if got := p.Tier(argv); got != "ask" {
			t.Errorf("Tier(%q) = %q, want ask (bare interpreter whitelist must not allow)", argv, got)
		}
	}
	withArgs := Policy{
		Whitelist: [][]string{{"python3", "safe.py"}},
		Default:   "ask",
	}
	if got := withArgs.Tier([]string{"python3", "safe.py"}); got != "allow" {
		t.Errorf("Tier(python3 safe.py) = %q, want allow (explicit args)", got)
	}
	deny := Policy{
		Blacklist: [][]string{{"curl"}},
		Default:   "ask",
	}
	if got := deny.Tier([]string{"curl", "@/etc/shadow"}); got != "deny" {
		t.Errorf("Tier(curl exfil) = %q, want deny (blacklist bare still denies)", got)
	}
}

func TestMatchWildStandaloneRejected(t *testing.T) {
	for _, rule := range [][]string{{"*"}, {"*", "*"}} {
		if matchWild([]string{"/bin/ls"}, rule) {
			t.Errorf("matchWild(ls, %q) = true, want false (catch-all rejected)", rule)
		}
		if matchWild([]string{"/bin/rm", "-rf", "/"}, rule) {
			t.Errorf("matchWild(rm, %q) = true, want false (catch-all rejected)", rule)
		}
	}
	p := Policy{
		Whitelist: [][]string{{"*"}},
		Default:   "ask",
	}
	if got := p.Tier([]string{"/bin/ls"}); got != "ask" {
		t.Errorf("Tier(ls) with [*] whitelist = %q, want ask", got)
	}
}

func TestMatchWildAnchoredPrefixStillWorks(t *testing.T) {
	p := Policy{
		Whitelist: [][]string{{"/bin/echo", "*"}},
		Default:   "ask",
	}
	if got := p.Tier([]string{"/bin/echo", "anything"}); got != "allow" {
		t.Errorf("Tier(echo anything) = %q, want allow (anchored prefix)", got)
	}
	if got := p.Tier([]string{"/bin/ls", "anything"}); got != "ask" {
		t.Errorf("Tier(ls anything) = %q, want ask", got)
	}
}

func TestAddRuleRejectsRestrictedBaseWhitelist(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/policy.json"
	if _, _, err := AddRule(path, "whitelist", "base", []string{"python3"}); err == nil {
		t.Error("AddRule whitelist base python3 must fail")
	}
	if _, _, err := AddRule(path, "blacklist", "base", []string{"python3"}); err != nil {
		t.Errorf("AddRule blacklist base python3 must succeed, got %v", err)
	}
	if _, _, err := AddRule(path, "whitelist", "exact", []string{"python3", "safe.py"}); err != nil {
		t.Errorf("AddRule whitelist exact with args must succeed, got %v", err)
	}
}
