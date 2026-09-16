package service

import (
	"os"
	"strings"
	"testing"

	"2fado/internal/policy"
)

func TestRootExecAllowedTable(t *testing.T) {
	cases := []struct {
		name   string
		target uint32
		allow  bool
		euid   uint32
		want   bool
	}{
		{"uid0 default refuses root euid", 0, false, 0, false},
		{"uid0 default refuses nonroot euid", 0, false, 1000, false},
		{"uid0 opt-in allows root euid", 0, true, 0, true},
		{"uid0 opt-in allows nonroot euid", 0, true, 1000, true},
		{"nonroot unaffected default", 1000, false, 0, true},
		{"nonroot unaffected opt-in", 1000, true, 1000, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rootExecAllowed(tc.target, tc.allow, tc.euid); got != tc.want {
				t.Errorf("rootExecAllowed(%d,%v,%d)=%v want %v", tc.target, tc.allow, tc.euid, got, tc.want)
			}
		})
	}
}

func TestExecuteRefusesNonSelfTarget(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("non-root behavior test; skipped as root")
	}
	s := testService(t, policy.Policy{Target: "0"})
	code, out, asUID := s.ExecuteWithChallenge([]string{"true"}, t.TempDir(), map[string]string{}, nil)
	if code == 0 {
		t.Fatal("expected non-zero refusal for non-self target")
	}
	if !strings.Contains(out, "escalation is not in this release") {
		t.Fatalf("refusal must state escalation is not in this release, got %q", out)
	}
	if asUID != uint32(os.Geteuid()) {
		t.Fatalf("refusal asUID must be daemon user, got %d", asUID)
	}
	data, err := os.ReadFile(s.Conf.StateDir + "/audit.log")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "escalation_disabled") {
		t.Fatal("expected escalation_disabled audit event")
	}
}

func TestExecuteSelfTargetUnaffected(t *testing.T) {
	s := testService(t, policy.Policy{})
	code, _, asUID := s.ExecuteWithChallenge([]string{"true"}, t.TempDir(), map[string]string{}, nil)
	if code != 0 {
		t.Fatalf("self-uid exec must succeed, got code %d", code)
	}
	if asUID != uint32(os.Geteuid()) {
		t.Fatalf("asUID must be daemon user, got %d", asUID)
	}
}

func TestExecuteOptInDoesNotEnableEscalation(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("non-root behavior test; skipped as root")
	}
	s := testService(t, policy.Policy{Target: "0"})
	s.Conf.AllowRootExec = true
	code, _, _ := s.ExecuteWithChallenge([]string{"true"}, t.TempDir(), map[string]string{}, nil)
	if code == 0 {
		t.Fatal("AllowRootExec must not enable escalation in this release")
	}
}
