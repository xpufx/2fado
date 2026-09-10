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
