package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateAnchorRefusesSymlink(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(real, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := ValidateAnchorDir(link); err == nil {
		t.Fatal("symlinked anchor must be refused")
	}
}

func TestDefaultsOutsideTmp(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("XDG_RUNTIME_DIR", "")
	if got := DefaultStateDir(); len(got) >= 4 && got[:4] == "/tmp" {
		t.Fatalf("state default in /tmp: %s", got)
	}
}
