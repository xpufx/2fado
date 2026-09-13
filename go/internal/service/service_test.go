package service

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
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

func TestCloseCardShowsResolutionSource(t *testing.T) {
	var gotText string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if strings.HasSuffix(r.URL.Path, "/editMessageText") {
			gotText = r.Form.Get("text")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"ok":true,"result":{"message_id":7,"chat":{"id":1}}}`)
	}))
	defer ts.Close()

	dir := t.TempDir()
	svc := New(config.Conf{StateDir: dir, Timeout: 5, BotToken: "test-token"})
	svc.TG.BaseURL = ts.URL
	if svc.getState() != nil {
		svc.getState().tgClient.BaseURL = ts.URL
	}
	if err := svc.Store.Init(); err != nil {
		t.Fatal(err)
	}
	rid := "close-card-1"
	if err := svc.Store.Save(protocol.PendingRecord{
		Argv: []string{"/bin/echo", "hi"}, UID: 1000, Cwd: "/tmp",
		Expires: time.Now().Add(60 * time.Second).Unix(),
	}, rid); err != nil {
		t.Fatal(err)
	}
	svc.Store.AttachPager(rid, "123", 7)

	svc.closeCard(rid, protocol.RunRequest{Argv: []string{"/bin/echo", "hi"}, Cwd: "/tmp"}, 1000, "approve", "paseo")
	if !strings.Contains(gotText, "✅ <b>Approved via Paseo Desktop</b>") {
		t.Fatalf("closed card missing Paseo source header, got:\n%s", gotText)
	}

	svc.closeCard(rid, protocol.RunRequest{Argv: []string{"/bin/echo", "hi"}, Cwd: "/tmp"}, 1000, "deny", "telegram:alice")
	if !strings.Contains(gotText, "❌ <b>Denied via Telegram (@alice)</b>") {
		t.Fatalf("closed card missing Telegram source header, got:\n%s", gotText)
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

func TestScrubEnvDropsLinkerShellOverrides(t *testing.T) {
	dangerous := []string{
		"LD_PRELOAD",
		"GLIBC_TUNABLES",
		"GCONV_PATH",
		"DYLD_INSERT_LIBRARIES",
		"IFS",
		"BASH_FUNC_foo",
		"BASHOPTS",
		"SHELLOPTS",
		"HOSTALIASES",
	}
	keep := append([]string{"TERM"}, dangerous...)
	svc := testService(t, policy.Policy{Default: "ask", EnvKeep: keep})
	env := map[string]string{"TERM": "xterm"}
	for _, k := range dangerous {
		env[k] = "evil"
	}
	clean, _ := svc.ScrubEnv(env)
	for _, k := range dangerous {
		if _, ok := clean[k]; ok {
			t.Errorf("%s survived scrub despite EnvKeep", k)
		}
	}
	if clean["TERM"] != "xterm" {
		t.Errorf("TERM should survive, got %q", clean["TERM"])
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
	}, uint32(os.Getuid()))
	if !res.Success {
		t.Fatalf("PolicyAddRule failed: %s", res.Error)
	}
	got := svc.Run(protocol.RunRequest{Argv: []string{"/bin/true"}, Cwd: t.TempDir(), Env: map[string]string{}}, 1000)
	if got.Status != "allowed" {
		t.Fatalf("after whitelist exact rule: Run = %+v, want allowed", got)
	}
	res = svc.PolicyAddRule(protocol.PolicyAddRuleRequest{
		Target: "blacklist", MatchType: "base", Pattern: []string{"/bin/echo"},
	}, uint32(os.Getuid()))
	if !res.Success {
		t.Fatalf("PolicyAddRule base failed: %s", res.Error)
	}
	got = svc.Run(protocol.RunRequest{Argv: []string{"/bin/echo", "anything"}, Cwd: t.TempDir(), Env: map[string]string{}}, 1000)
	if got.Status != "denied" {
		t.Fatalf("after blacklist base rule: Run = %+v, want denied", got)
	}
	res = svc.PolicyAddRule(protocol.PolicyAddRuleRequest{
		Target: "blacklist", MatchType: "custom", Pattern: []string{"/bin/ls", "*"},
	}, uint32(os.Getuid()))
	if !res.Success {
		t.Fatalf("PolicyAddRule custom failed: %s", res.Error)
	}
	got = svc.Run(protocol.RunRequest{Argv: []string{"/bin/ls", "-la", "/tmp"}, Cwd: t.TempDir(), Env: map[string]string{}}, 1000)
	if got.Status != "denied" {
		t.Fatalf("after blacklist custom rule: Run = %+v, want denied", got)
	}
	bad := svc.PolicyAddRule(protocol.PolicyAddRuleRequest{Target: "nope", MatchType: "exact", Pattern: []string{"/bin/x"}}, uint32(os.Getuid()))
	if bad.Success {
		t.Error("bad target must fail")
	}
}

func TestAdoptOrphansExecutesOnApproval(t *testing.T) {
	svc := testService(t, policy.Policy{Default: "ask"})
	svc.Conf.Timeout = 10
	svc.Conf.DryRun = false

	rid := "orphan-adopt-1"
	rec := protocol.PendingRecord{
		Argv:    []string{"/bin/echo", "adopted"},
		UID:     1000,
		Cwd:     t.TempDir(),
		Expires: time.Now().Add(8 * time.Second).Unix(),
		Step:    "initial",
	}
	if err := svc.Store.Save(rec, rid); err != nil {
		t.Fatal(err)
	}

	svc.AdoptOrphans()

	if !svc.Store.Consume(rid, "approve", "operator") {
		t.Fatal("failed to consume orphan approval")
	}

	deadline := time.Now().Add(6 * time.Second)
	for {
		st := svc.Store.Status(rid, time.Now().Unix())
		if st.Status == "completed" {
			break
		}
		done := false
		for _, it := range svc.Store.Recent(10) {
			if it.ID == rid && it.Exit == 0 {
				done = true
				break
			}
		}
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("orphan petition not executed after approval, status=%+v", st)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestAdoptOrphansSkipsExpired(t *testing.T) {
	svc := testService(t, policy.Policy{Default: "ask"})

	rid := "orphan-expired-1"
	rec := protocol.PendingRecord{
		Argv:    []string{"/bin/echo", "stale"},
		UID:     1000,
		Cwd:     t.TempDir(),
		Expires: time.Now().Add(-1 * time.Second).Unix(),
		Step:    "initial",
	}
	if err := svc.Store.Save(rec, rid); err != nil {
		t.Fatal(err)
	}

	svc.AdoptOrphans()
	time.Sleep(300 * time.Millisecond)

	if items := svc.Store.List(time.Now().Unix()); len(items) != 0 {
		t.Fatalf("expired orphan still listed: %+v", items)
	}
}

func TestRunDetachedExecutesOnApproval(t *testing.T) {
	svc := testService(t, policy.Policy{Default: "ask"})
	svc.Conf.Timeout = 10
	svc.Conf.DryRun = true

	type runOut struct {
		res protocol.RunResult
	}
	done := make(chan runOut, 1)
	go func() {
		res := svc.RunWithCancel(protocol.RunRequest{
			Argv:   []string{"/bin/echo", "detached"},
			Cwd:    t.TempDir(),
			Env:    map[string]string{},
			Detach: true,
		}, 1000, nil)
		done <- runOut{res: res}
	}()

	var out runOut
	select {
	case out = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("detached Run did not return immediately")
	}
	if out.res.Status != "pending" || out.res.ID == "" {
		t.Fatalf("RunResult = %+v, want pending with ID", out.res)
	}

	if !svc.Store.Consume(out.res.ID, "approve", "operator") {
		t.Fatal("failed to consume detached approval")
	}

	deadline := time.Now().Add(6 * time.Second)
	for {
		st := svc.Store.Status(out.res.ID, time.Now().Unix())
		if st.Status == "completed" {
			if st.Exit != 0 {
				t.Fatalf("detached status = %+v, want exit 0", st)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("detached request not executed after approval, status=%+v", st)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestRunDetachedDenyRecordsDenied(t *testing.T) {
	svc := testService(t, policy.Policy{Default: "ask"})
	svc.Conf.Timeout = 10

	res := svc.RunWithCancel(protocol.RunRequest{
		Argv:   []string{"/bin/echo", "detached-deny"},
		Cwd:    t.TempDir(),
		Env:    map[string]string{},
		Detach: true,
	}, 1000, nil)
	if res.Status != "pending" || res.ID == "" {
		t.Fatalf("RunResult = %+v, want pending with ID", res)
	}

	if !svc.Store.Consume(res.ID, "deny", "operator") {
		t.Fatal("failed to consume detached deny")
	}

	deadline := time.Now().Add(6 * time.Second)
	for {
		st := svc.Store.Status(res.ID, time.Now().Unix())
		if st.Status == "denied" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("detached deny not recorded, status=%+v", st)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

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

func TestExecuteUnknownTargetUserFailsClean(t *testing.T) {
	svc := testService(t, policy.Policy{Default: "allow", Target: "4294967294"})
	code, out, _ := svc.Execute([]string{"/bin/true"}, t.TempDir(), map[string]string{})
	if code == 0 {
		t.Errorf("expected failure for unknown target user, got exit 0 (out=%q)", out)
	}
}

func unprivilegedUID() uint32 {
	self := uint32(os.Getuid())
	for _, c := range []uint32{65000, 65001, nobodyUID(), 1} {
		if c != 0 && c != self {
			return c
		}
	}
	return self + 1
}

func nobodyUID() uint32 {
	u, err := user.Lookup("nobody")
	if err != nil {
		return 65534
	}
	n, _ := strconv.Atoi(u.Uid)
	return uint32(n)
}

func TestSubmitRejectsUnprivilegedUID(t *testing.T) {
	svc := testService(t, policy.Policy{Default: "ask"})
	uid := unprivilegedUID()
	ack := svc.Submit(protocol.VerdictSubmit{ID: "rid-x", Decision: "approve", By: "spoofed"}, uid)
	if ack.Recorded || ack.Error != "unauthorized" {
		t.Fatalf("unauthorized verdict = %+v, want rejection", ack)
	}
}

func TestSubmitAllowsOperatorUID(t *testing.T) {
	svc := testService(t, policy.Policy{Default: "ask"})
	rid := "auth-ok-1"
	if err := svc.Store.Save(protocol.PendingRecord{
		Argv: []string{"/bin/echo", "hi"}, UID: 1000, Cwd: t.TempDir(),
		Expires: time.Now().Add(30 * time.Second).Unix(),
	}, rid); err != nil {
		t.Fatal(err)
	}
	ack := svc.Submit(protocol.VerdictSubmit{ID: rid, Decision: "approve", By: "op"}, uint32(os.Getuid()))
	if !ack.Recorded || ack.Error != "" {
		t.Fatalf("operator verdict = %+v, want recorded", ack)
	}
}

func TestPolicyMutationRejectsUnprivilegedUID(t *testing.T) {
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
	uid := unprivilegedUID()
	addRes := svc.PolicyAddRule(protocol.PolicyAddRuleRequest{
		Target: "whitelist", MatchType: "exact", Pattern: []string{"/bin/bash"},
	}, uid)
	if addRes.Success || addRes.Error != "unauthorized" {
		t.Fatalf("unauthorized policy_add_rule = %+v, want rejection", addRes)
	}
	cfgRes := svc.TelegramSetConfig(protocol.TelegramSetConfigRequest{}, uid)
	if cfgRes.Success || cfgRes.Error != "unauthorized" {
		t.Fatalf("unauthorized telegram_set_config = %+v, want rejection", cfgRes)
	}
}

func TestAdoptOrphansPreservesEnv(t *testing.T) {
	printenv, err := exec.LookPath("printenv")
	if err != nil {
		t.Skip("printenv not available")
	}
	svc := testService(t, policy.Policy{Default: "ask", EnvKeep: []string{"TWOFADO_ORPHAN_MARKER"}})
	svc.Conf.Timeout = 10

	rid := "orphan-env-1"
	rec := protocol.PendingRecord{
		Argv:    []string{printenv, "TWOFADO_ORPHAN_MARKER"},
		UID:     uint32(os.Getuid()),
		Cwd:     t.TempDir(),
		Expires: time.Now().Add(8 * time.Second).Unix(),
		Env:     map[string]string{"TWOFADO_ORPHAN_MARKER": "adopted-env-ok", "PATH": "/usr/bin:/bin"},
		Step:    "initial",
	}
	if err := svc.Store.Save(rec, rid); err != nil {
		t.Fatal(err)
	}

	svc.AdoptOrphans()

	if !svc.Store.Consume(rid, "approve", "operator") {
		t.Fatal("failed to consume orphan approval")
	}

	deadline := time.Now().Add(6 * time.Second)
	for {
		found := false
		for _, it := range svc.Store.Recent(10) {
			if it.ID == rid {
				found = true
				if it.Exit == 0 && strings.Contains(it.Output, "adopted-env-ok") {
					return
				}
				if it.Exit != 0 && it.Output != "" {
					t.Fatalf("orphan executed without preserved env: exit=%d out=%q", it.Exit, it.Output)
				}
			}
		}
		if time.Now().After(deadline) {
			if !found {
				t.Fatal("orphan petition produced no result after approval")
			}
			t.Fatal("orphan result missing preserved env marker")
		}
		time.Sleep(100 * time.Millisecond)
	}
}
