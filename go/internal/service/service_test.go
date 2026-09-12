package service

import (
	"os"
	"path/filepath"
	"testing"
	"time"

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

func TestStatusLifecycle(t *testing.T) {
	svc := testService(t, policy.Policy{Default: "ask"})

	// 1. not_found
	st := svc.Status("unknown-id")
	if st.Status != "not_found" {
		t.Fatalf("Status(unknown) = %q, want not_found", st.Status)
	}

	// 2. pending
	rid := "req-1"
	rec := protocol.PendingRecord{
		Argv:    []string{"/bin/echo", "test"},
		UID:     1000,
		Cwd:     "/tmp",
		Expires: 9999999999,
	}
	if err := svc.Store.Save(rec, rid); err != nil {
		t.Fatal(err)
	}
	st = svc.Status(rid)
	if st.Status != "pending" || st.ExpiresIn <= 0 || st.Argv[0] != "/bin/echo" {
		t.Fatalf("Status(pending) = %+v", st)
	}

	// 3. running (verdict consumed, but no result file yet)
	if !svc.Store.Consume(rid, "approve", "test-user") {
		t.Fatal("Consume failed")
	}
	st = svc.Status(rid)
	if st.Status != "running" || st.Decision != "approve" || st.By != "test-user" {
		t.Fatalf("Status(running) = %+v", st)
	}

	// 4. completed (result file written)
	svc.Store.SaveResult(rid, 0, "hello world")
	st = svc.Status(rid)
	if st.Status != "completed" || st.Exit != 0 || st.Output != "hello world" {
		t.Fatalf("Status(completed) = %+v", st)
	}

	// 5. denied
	rid2 := "req-2"
	rec2 := protocol.PendingRecord{Argv: []string{"/bin/ls"}, UID: 1000, Cwd: "/tmp", Expires: 9999999999}
	_ = svc.Store.Save(rec2, rid2)
	_ = svc.Store.Consume(rid2, "deny", "admin")
	svc.Store.SaveResult(rid2, -1, "")
	st = svc.Status(rid2)
	if st.Status != "denied" || st.Decision != "deny" || st.Exit != -1 {
		t.Fatalf("Status(denied) = %+v", st)
	}

	// 6. timeout
	rid3 := "req-3"
	rec3 := protocol.PendingRecord{Argv: []string{"/bin/ls"}, UID: 1000, Cwd: "/tmp", Expires: 100}
	_ = svc.Store.Save(rec3, rid3)
	st = svc.Status(rid3)
	if st.Status != "timeout" || st.Exit != -1 {
		t.Fatalf("Status(timeout) = %+v", st)
	}
}

func TestConfirmLifecycleApproved(t *testing.T) {
	svc := testService(t, policy.Policy{
		Confirm: [][]string{{"/bin/echo", "dangerous"}},
		Default: "ask",
	})
	svc.Conf.Timeout = 5

	type runOut struct {
		res protocol.RunResult
	}
	done := make(chan runOut, 1)
	go func() {
		res := svc.Run(protocol.RunRequest{
			Argv: []string{"/bin/echo", "dangerous"},
			Cwd:  t.TempDir(),
			Env:  map[string]string{},
		}, 1000)
		done <- runOut{res: res}
	}()

	// 1. Wait for step 1 petition
	var step1ID string
	for i := 0; i < 50; i++ {
		items := svc.Store.List(time.Now().Unix())
		if len(items) > 0 {
			step1ID = items[0].ID
			if items[0].Step != "initial" {
				t.Fatalf("step 1 Step = %q, want 'initial'", items[0].Step)
			}
			if items[0].ConfirmOf != "" {
				t.Fatalf("step 1 ConfirmOf = %q, want empty", items[0].ConfirmOf)
			}
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if step1ID == "" {
		t.Fatal("step 1 petition did not appear")
	}

	// Approve step 1
	if !svc.Store.Consume(step1ID, "approve", "operator") {
		t.Fatal("failed to consume step 1 approval")
	}

	// 2. Wait for step 2 confirmation petition
	var step2ID string
	for i := 0; i < 50; i++ {
		items := svc.Store.List(time.Now().Unix())
		for _, it := range items {
			if it.ID != step1ID && it.ConfirmOf == step1ID {
				step2ID = it.ID
				if it.Step != "confirm" {
					t.Fatalf("step 2 Step = %q, want 'confirm'", it.Step)
				}
				break
			}
		}
		if step2ID != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if step2ID == "" {
		t.Fatal("step 2 confirmation petition did not appear")
	}

	// Verify step 1 status is now "confirming"
	st1 := svc.Status(step1ID)
	if st1.Status != "confirming" {
		t.Fatalf("Status(step1ID) = %q, want 'confirming'", st1.Status)
	}

	// Approve step 2
	if !svc.Store.Consume(step2ID, "approve", "operator") {
		t.Fatal("failed to consume step 2 approval")
	}

	out := <-done
	if out.res.Status != "allowed" || out.res.Exit != 0 {
		t.Fatalf("RunResult = %+v, want allowed exit 0", out.res)
	}

	// Both step 1 and step 2 must now be completed
	st1Final := svc.Status(step1ID)
	if st1Final.Status != "completed" || st1Final.Exit != 0 {
		t.Fatalf("step 1 final status = %+v, want completed exit 0", st1Final)
	}
	st2Final := svc.Status(step2ID)
	if st2Final.Status != "completed" || st2Final.Exit != 0 {
		t.Fatalf("step 2 final status = %+v, want completed exit 0", st2Final)
	}
}

func TestConfirmLifecycleTimeout(t *testing.T) {
	svc := testService(t, policy.Policy{
		Confirm: [][]string{{"/bin/echo", "dangerous"}},
		Default: "ask",
	})
	svc.Conf.Timeout = 2 // 2 second timeout to avoid sub-second boundary truncation

	type runOut struct {
		res protocol.RunResult
	}
	done := make(chan runOut, 1)
	go func() {
		res := svc.Run(protocol.RunRequest{
			Argv: []string{"/bin/echo", "dangerous"},
			Cwd:  t.TempDir(),
			Env:  map[string]string{},
		}, 1000)
		done <- runOut{res: res}
	}()

	// 1. Wait for step 1 petition
	var step1ID string
	for i := 0; i < 50; i++ {
		items := svc.Store.List(time.Now().Unix())
		if len(items) > 0 {
			step1ID = items[0].ID
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if step1ID == "" {
		t.Fatal("step 1 petition did not appear")
	}

	// Approve step 1
	if !svc.Store.Consume(step1ID, "approve", "operator") {
		t.Fatal("failed to consume step 1 approval")
	}

	// 2. Wait for step 2 confirmation petition
	var step2ID string
	for i := 0; i < 50; i++ {
		items := svc.Store.List(time.Now().Unix())
		for _, it := range items {
			if it.ID != step1ID && it.ConfirmOf == step1ID {
				step2ID = it.ID
				break
			}
		}
		if step2ID != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if step2ID == "" {
		t.Fatal("step 2 confirmation petition did not appear")
	}

	// Do NOT approve step 2; wait for timeout
	out := <-done
	if out.res.Status != "denied" || out.res.Reason != "confirmation_timeout" {
		t.Fatalf("RunResult = %+v, want denied confirmation_timeout", out.res)
	}

	// Both step 1 and step 2 must reflect timeout/denied
	st1 := svc.Status(step1ID)
	if st1.Status != "timeout" || st1.Exit != -1 {
		t.Fatalf("step 1 status after timeout = %+v, want timeout exit -1", st1)
	}
	st2 := svc.Status(step2ID)
	if st2.Status != "timeout" || st2.Exit != -1 {
		t.Fatalf("step 2 status after timeout = %+v, want timeout exit -1", st2)
	}
}

func TestConfirmLifecycleClientAborted(t *testing.T) {
	svc := testService(t, policy.Policy{
		Confirm: [][]string{{"/bin/echo", "dangerous"}},
		Default: "ask",
	})
	svc.Conf.Timeout = 5

	cancel := make(chan struct{})
	type runOut struct {
		res protocol.RunResult
	}
	done := make(chan runOut, 1)
	go func() {
		res := svc.RunWithCancel(protocol.RunRequest{
			Argv: []string{"/bin/echo", "dangerous"},
			Cwd:  t.TempDir(),
			Env:  map[string]string{},
		}, 1000, cancel)
		done <- runOut{res: res}
	}()

	// 1. Wait for step 1 petition
	var step1ID string
	for i := 0; i < 50; i++ {
		items := svc.Store.List(time.Now().Unix())
		if len(items) > 0 {
			step1ID = items[0].ID
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if step1ID == "" {
		t.Fatal("step 1 petition did not appear")
	}

	// Approve step 1
	if !svc.Store.Consume(step1ID, "approve", "operator") {
		t.Fatal("failed to consume step 1 approval")
	}

	// 2. Wait for step 2 confirmation petition
	var step2ID string
	for i := 0; i < 50; i++ {
		items := svc.Store.List(time.Now().Unix())
		for _, it := range items {
			if it.ID != step1ID && it.ConfirmOf == step1ID {
				step2ID = it.ID
				break
			}
		}
		if step2ID != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if step2ID == "" {
		t.Fatal("step 2 confirmation petition did not appear")
	}

	// Simulate client socket disconnect / Ctrl+C
	close(cancel)

	out := <-done
	if out.res.Status != "denied" || out.res.Reason != "client_aborted" {
		t.Fatalf("RunResult = %+v, want denied client_aborted", out.res)
	}

	// Pending list must now be empty (purged)
	items := svc.Store.List(time.Now().Unix())
	if len(items) != 0 {
		t.Fatalf("Pending items after abort = %+v, want empty", items)
	}

	// Both step 1 and step 2 must reflect client_aborted
	st1 := svc.Status(step1ID)
	if st1.Status != "client_aborted" || st1.Exit != -1 {
		t.Fatalf("step 1 status after abort = %+v, want client_aborted", st1)
	}
	st2 := svc.Status(step2ID)
	if st2.Status != "client_aborted" || st2.Exit != -1 {
		t.Fatalf("step 2 status after abort = %+v, want client_aborted", st2)
	}
}

func TestPolicyAddRuleLiveReload(t *testing.T) {
	dir := t.TempDir()
	policyPath := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(policyPath, []byte(`{"mode":"blacklist","default":"ask"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	st := store.New(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	svc := Service{
		Conf:      config.Conf{StateDir: dir, Timeout: 5, Policy: policyPath},
		Policy:    policy.Load(policyPath),
		Store:     st,
		Approvers: map[string]bool{},
		Verdicts:  make(chan telegram.Verdict, 64),
	}
	res := svc.PolicyAddRule(protocol.PolicyAddRuleRequest{
		Target: "whitelist", MatchType: "exact", Pattern: []string{"/bin/true"},
	})
	if !res.Success {
		t.Fatalf("PolicyAddRule failed: %s", res.Error)
	}
	got := svc.Run(protocol.RunRequest{Argv: []string{"/bin/true"}, Cwd: t.TempDir(), Env: map[string]string{}}, 1000)
	if got.Status != "allowed" {
		t.Fatalf("after whitelist exact rule: Run = %+v, want allowed", got)
	}
	res = svc.PolicyAddRule(protocol.PolicyAddRuleRequest{
		Target: "blacklist", MatchType: "base", Pattern: []string{"/bin/echo"},
	})
	if !res.Success {
		t.Fatalf("PolicyAddRule base failed: %s", res.Error)
	}
	got = svc.Run(protocol.RunRequest{Argv: []string{"/bin/echo", "anything"}, Cwd: t.TempDir(), Env: map[string]string{}}, 1000)
	if got.Status != "denied" {
		t.Fatalf("after blacklist base rule: Run = %+v, want denied", got)
	}
	res = svc.PolicyAddRule(protocol.PolicyAddRuleRequest{
		Target: "blacklist", MatchType: "custom", Pattern: []string{"/bin/ls", "*"},
	})
	if !res.Success {
		t.Fatalf("PolicyAddRule custom failed: %s", res.Error)
	}
	got = svc.Run(protocol.RunRequest{Argv: []string{"/bin/ls", "-la", "/tmp"}, Cwd: t.TempDir(), Env: map[string]string{}}, 1000)
	if got.Status != "denied" {
		t.Fatalf("after blacklist custom rule: Run = %+v, want denied", got)
	}
	bad := svc.PolicyAddRule(protocol.PolicyAddRuleRequest{Target: "nope", MatchType: "exact", Pattern: []string{"/bin/x"}})
	if bad.Success {
		t.Error("bad target must fail")
	}
}
