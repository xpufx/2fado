package service

import (
	"testing"

	"2fado/internal/config"
	"2fado/internal/policy"
	"2fado/internal/protocol"
	"2fado/internal/store"
	"2fado/internal/telegram"
)

func testService(t *testing.T, p policy.Policy) Service {
	t.Helper()
	dir := t.TempDir()
	st := store.New(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	return Service{
		Conf:      config.Conf{StateDir: dir, Timeout: 5},
		Policy:    p,
		Store:     st,
		Approvers: map[string]bool{},
		Verdicts:  make(chan telegram.Verdict, 64),
	}
}

func TestScrubEnvDropsAttackVars(t *testing.T) {
	svc := testService(t, policy.Policy{Default: "ask"})
	env := map[string]string{
		"TERM":              "xterm",
		"LD_PRELOAD":        "/tmp/evil.so",
		"LD_LIBRARY_PATH":   "/tmp",
		"PYTHONPATH":        "/tmp",
		"PYTHONHOME":        "/tmp",
		"PERL5LIB":          "/tmp",
		"RUBYOPT":           "-r/tmp/evil",
		"NODE_OPTIONS":      "--require /tmp/evil",
		"BASH_ENV":          "/tmp/evil.sh",
		"ENV":               "/tmp/evil.sh",
		"CDPATH":            "/tmp",
		"HOME":              "/tmp/fakehome",
		"GIT_SSH":           "/tmp/evil-ssh",
		"AWS_SECRET":        "x",
		"PATH":              "/tmp/evilbin",
		"GLIBC_TUNABLES":    "x",
		"JAVA_TOOL_OPTIONS": "-javaagent:/tmp/evil.jar",
	}
	clean, dropped := svc.ScrubEnv(env)
	if clean["PATH"] != safePath {
		t.Errorf("PATH = %q, want %q", clean["PATH"], safePath)
	}
	if clean["TERM"] != "xterm" {
		t.Errorf("TERM should survive, got %q", clean["TERM"])
	}
	for _, k := range []string{"LD_PRELOAD", "PYTHONPATH", "PERL5LIB", "RUBYOPT",
		"NODE_OPTIONS", "BASH_ENV", "ENV", "CDPATH", "HOME", "GIT_SSH"} {
		if _, ok := clean[k]; ok {
			t.Errorf("%s survived scrub", k)
		}
	}
	if len(dropped) == 0 {
		t.Error("expected dropped list for audit log")
	}
}

func TestScrubEnvKeepCannotOverrideDeny(t *testing.T) {
	svc := testService(t, policy.Policy{Default: "ask", EnvKeep: []string{"PYTHONPATH", "MYAPP_FLAG"}})
	clean, _ := svc.ScrubEnv(map[string]string{
		"PYTHONPATH": "/tmp",
		"MYAPP_FLAG": "1",
	})
	if _, ok := clean["PYTHONPATH"]; ok {
		t.Error("env_deny must win over env_keep for PYTHONPATH")
	}
	if clean["MYAPP_FLAG"] != "1" {
		t.Error("explicit env_keep should survive")
	}
}

func TestRunAllowTierExecutesWithoutPaging(t *testing.T) {
	svc := testService(t, policy.Policy{
		Whitelist: [][]string{{"/bin/true"}},
		Default:   "ask",
	})
	res := svc.Run(protocol.RunRequest{Argv: []string{"/bin/true"}, Cwd: t.TempDir(), Env: map[string]string{}}, 1000)
	if res.Status != "allowed" || res.Exit != 0 {
		t.Fatalf("allow tier = %+v, want allowed exit 0", res)
	}
}

func TestRunDenyTierRefusesWithoutExec(t *testing.T) {
	svc := testService(t, policy.Policy{
		Blacklist: [][]string{{"/bin/echo", "hi", ";", "rm", "-rf", "/"}},
		Whitelist: [][]string{{"/bin/echo", "hi"}},
		Default:   "ask",
	})
	chained := []string{"/bin/echo", "hi", ";", "rm", "-rf", "/"}
	if tier := svc.Policy.Tier(chained); tier != "deny" {
		t.Fatalf("Tier(chained) = %q, want deny", tier)
	}
	res := svc.Run(protocol.RunRequest{Argv: chained, Cwd: t.TempDir(), Env: map[string]string{}}, 1000)
	if res.Status != "denied" || res.Reason != "policy" {
		t.Fatalf("deny tier = %+v, want denied/policy", res)
	}
}
