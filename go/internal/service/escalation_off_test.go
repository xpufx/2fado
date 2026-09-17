//go:build !escalation

package service

import (
	"go/build"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"2fado/internal/policy"
)

// TestDefaultBuildExcludesEscalationSource is the #61 release guard: the
// default (public) build must not compile the privilege-switching file,
// so the shipped binary cannot contain the escalation code.
func TestDefaultBuildExcludesEscalationSource(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var hasOn, hasOff bool
	for _, f := range files {
		switch f {
		case "escalation_on.go":
			hasOn = true
		case "escalation_off.go":
			hasOff = true
		}
	}
	if !hasOff {
		t.Fatal("escalation_off.go missing: default build has no seam")
	}
	if hasOn {
		// Present on disk is fine; what matters is that the build tag
		// excludes it. Confirm via go/build's tag evaluation.
		t.Log("escalation_on.go present on disk (expected; build-tagged out)")
	}

	// Authoritative: ask the Go toolchain which files this tag set builds.
	ctx := build.Default
	ctx.UseAllFiles = false
	pkg, err := ctx.ImportDir(".", 0)
	if err != nil && !strings.Contains(err.Error(), "no buildable Go source") {
		// ImportDir can complain about test files; fall back to GoFiles.
		t.Logf("ImportDir: %v", err)
	}
	if pkg != nil {
		for _, f := range pkg.GoFiles {
			if f == "escalation_on.go" {
				t.Fatalf("escalation_on.go compiled into the default build (#61 violation)")
			}
		}
		found := false
		for _, f := range pkg.GoFiles {
			if f == "escalation_off.go" {
				found = true
			}
		}
		if !found {
			t.Fatal("escalation_off.go not compiled into the default build")
		}
	}
}

// TestEscalationOffSeamIsInert pins the default build's semantics: no
// credential is ever produced, identity is always the daemon's own euid,
// and escalationEnabled is false.
func TestEscalationOffSeamIsInert(t *testing.T) {
	if escalationEnabled {
		t.Fatal("escalationEnabled must be false in the default build")
	}
	svc := testService(t, policy.Policy{})
	attrs, err := svc.spawnAttrs(0)
	if err != nil {
		t.Fatalf("spawnAttrs: %v", err)
	}
	if attrs != nil {
		t.Fatalf("default build must never attach a credential-bearing attr, got %+v", attrs)
	}
	if got := svc.displayUID(); got != uint32(os.Geteuid()) {
		t.Fatalf("displayUID = %d, want daemon euid %d", got, os.Geteuid())
	}
}

// TestEscalationOffRefusesForeignUID covers the fail-closed refusal: a
// policy target that resolves to another uid must refuse, not spawn.
func TestEscalationOffRefusesForeignUID(t *testing.T) {
	svc := testService(t, policy.Policy{Target: "0"})
	if uid, refusal := svc.execIdentity([]string{"/bin/echo", "x"}, "."); refusal == nil {
		t.Fatalf("uid-0 target must be refused in the default build (got uid=%d, no refusal)", uid)
	} else if uid != uint32(os.Geteuid()) {
		t.Fatalf("refusal should report the daemon euid, got %d", uid)
	}
}
