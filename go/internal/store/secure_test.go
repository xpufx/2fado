package store

import (
	"os"
	"path/filepath"
	"testing"

	"2fado/internal/protocol"
)

func TestSymlinkWriteRefused(t *testing.T) {
	st := New(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "evil")
	_ = os.WriteFile(target, []byte("x"), 0o600)
	link := filepath.Join(st.Pending, "rid1.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	rec := protocol.PendingRecord{Argv: []string{"/bin/echo"}, Expires: 9999999999}
	if err := st.Save(rec, "rid1"); err == nil {
		t.Fatal("symlink write must be refused")
	}
}

func TestForeignDirRefused(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(real, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	st := New(link)
	if err := st.Init(); err == nil {
		t.Fatal("symlinked anchor must refuse startup")
	}
}
