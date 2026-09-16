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

func TestExecuteWithChallengeDefaultRefusesUID0(t *testing.T) {
	s := testService(t, policy.Policy{Target: "0"})
	code, out, _ := s.ExecuteWithChallenge([]string{"true"}, t.TempDir(), map[string]string{}, nil)
	if code == 0 {
		t.Fatal("expected non-zero refusal for uid-0 default")
	}
	if !strings.Contains(out, "--allow-root") || !strings.Contains(out, "ALLOW_ROOT") {
		t.Fatalf("refusal must name the opt-in, got %q", out)
	}
	events := s.Store.Recent(50)
	_ = events
	data, err := os.ReadFile(s.Conf.StateDir + "/audit.log")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "root_exec_blocked") {
		t.Fatal("expected root_exec_blocked audit event")
	}
}

func TestExecuteWithChallengeOptInAllowsUID0Gate(t *testing.T) {
	if !rootExecAllowed(0, true, 1000) {
		t.Fatal("opt-in must allow uid-0")
	}
}
