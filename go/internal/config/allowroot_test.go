package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseAllowRootFailClosed(t *testing.T) {
	for raw, want := range map[string]bool{
		"1": true, "true": true, "yes": true, "TRUE": true, " Yes ": true,
		"": false, "0": false, "false": false, "no": false, "maybe": false, "2": false,
	} {
		if got := ParseAllowRoot(raw); got != want {
			t.Errorf("ParseAllowRoot(%q)=%v want %v", raw, got, want)
		}
	}
}

func TestLoadAllowRootDefaultFalse(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "c.conf")
	if err := os.WriteFile(f, []byte("SOCKET=/tmp/x.sock\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TWOFADO_USER_CONFIG", filepath.Join(dir, "uc.json"))
	if c := Load(f); c.AllowRootExec {
		t.Fatal("default must be false")
	}
	if err := os.WriteFile(f, []byte("ALLOW_ROOT=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if c := Load(f); !c.AllowRootExec {
		t.Fatal("ALLOW_ROOT=1 must enable")
	}
	if err := os.WriteFile(f, []byte("ALLOW_ROOT=maybe\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if c := Load(f); c.AllowRootExec {
		t.Fatal("ambiguous value must fail closed to false")
	}
}
