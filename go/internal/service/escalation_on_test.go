//go:build escalation

package service

import (
	"os"
	"os/user"
	"strconv"
	"testing"
)

// resolveCredential exists only in the escalation build (#61).

func TestResolveCredentialCurrentUser(t *testing.T) {
	uid := uint32(os.Getuid())
	cred, err := resolveCredential(uid)
	if err != nil {
		t.Fatalf("resolveCredential(%d) failed: %v", uid, err)
	}
	if cred.Uid != uid {
		t.Errorf("Uid = %d, want %d", cred.Uid, uid)
	}
	cur, err := user.LookupId(strconv.Itoa(os.Getuid()))
	if err != nil {
		t.Skipf("cannot look up current user: %v", err)
	}
	wantGid, _ := strconv.Atoi(cur.Gid)
	if int(cred.Gid) != wantGid {
		t.Errorf("Gid = %d, want primary gid %d (not uid)", cred.Gid, wantGid)
	}
	if cred.Groups == nil {
		t.Error("Groups must be non-nil so the child drops caller groups")
	}
}

func TestResolveCredentialUnknownUser(t *testing.T) {
	if _, err := resolveCredential(4294967294); err == nil {
		t.Error("expected error for nonexistent uid, got nil")
	}
}
