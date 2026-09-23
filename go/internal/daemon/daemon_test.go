package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"2fado/internal/client"
	"2fado/internal/config"
	"2fado/internal/protocol"
	"2fado/internal/service"
)

func TestDaemonTelegramSocketEndpoints(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/botmy-socket-token/getMe" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintln(w, `{"ok":true,"result":{"id":777,"is_bot":true,"username":"socket_bot"}}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.CloseClientConnections()
	defer ts.Close()

	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "test.sock")
	cfgPath := filepath.Join(tmpDir, "config.json")
	t.Setenv("TWOFADO_USER_CONFIG", cfgPath)

	cfg := config.Conf{
		Socket:   sockPath,
		StateDir: tmpDir,
		Timeout:  5,
		Policy:   filepath.Join(tmpDir, "policy.json"),
	}
	svc := service.New(cfg)
	svc.TG.BaseURL = ts.URL

	// Start daemon in background
	go func() {
		_ = Serve(svc)
	}()

	// Wait for socket to become available
	var ready bool
	var lastErr error
	for i := 0; i < 50; i++ {
		conn, err := net.Dial("unix", sockPath)
		if err == nil {
			conn.Close()
			ready = true
			break
		}
		lastErr = err
		time.Sleep(20 * time.Millisecond)
	}
	if !ready {
		t.Fatalf("failed to connect to daemon socket: %v", lastErr)
	}

	sendAndRecv := func(req string) []byte {
		c, err := net.Dial("unix", sockPath)
		if err != nil {
			t.Fatalf("dial failed: %v", err)
		}
		defer c.Close()
		if _, err := c.Write([]byte(req + "\n")); err != nil {
			t.Fatalf("write failed: %v", err)
		}
		line, err := bufio.NewReader(c).ReadBytes('\n')
		if err != nil {
			t.Fatalf("read failed: %v", err)
		}
		return line
	}

	// 1. Query telegram_info before configuration
	t.Log("Step 1: telegram_info")
	infoBytes := sendAndRecv(`{"telegram_info":{}}`)
	t.Log("Step 1 done")
	var infoRes protocol.TelegramInfoResponse
	if err := json.Unmarshal(infoBytes, &infoRes); err != nil {
		t.Fatalf("unmarshal info failed: %v", err)
	}
	if infoRes.Configured {
		t.Errorf("expected Configured false initially")
	}
	if infoRes.Status != "unconfigured" {
		t.Errorf("expected status 'unconfigured', got %q", infoRes.Status)
	}

	// 2. Send telegram_set_config over socket
	t.Log("Step 2: telegram_set_config")
	setReq := `{"telegram_set_config":{"bot_token":"my-socket-token","chat_id":"-999","approvers":["100","200"]}}`
	setBytes := sendAndRecv(setReq)
	t.Log("Step 2 done")
	var setRes protocol.TelegramSetConfigResponse
	if err := json.Unmarshal(setBytes, &setRes); err != nil {
		t.Fatalf("unmarshal set_config failed: %v", err)
	}
	if !setRes.Success {
		t.Fatalf("expected set_config success, got error: %s", setRes.Error)
	}
	if setRes.BotUsername != "socket_bot" {
		t.Errorf("expected BotUsername 'socket_bot', got %q", setRes.BotUsername)
	}

	// 3. Query telegram_info after configuration
	infoAfterBytes := sendAndRecv(`{"telegram_info":{}}`)
	var infoAfterRes protocol.TelegramInfoResponse
	if err := json.Unmarshal(infoAfterBytes, &infoAfterRes); err != nil {
		t.Fatalf("unmarshal info after failed: %v", err)
	}
	if !infoAfterRes.Configured {
		t.Errorf("expected Configured true")
	}
	if infoAfterRes.Status != "connected" {
		t.Errorf("expected status 'connected', got %q", infoAfterRes.Status)
	}
	if infoAfterRes.BotUsername != "socket_bot" {
		t.Errorf("expected BotUsername 'socket_bot', got %q", infoAfterRes.BotUsername)
	}
	if infoAfterRes.ChatID != "-999" {
		t.Errorf("expected ChatID '-999', got %q", infoAfterRes.ChatID)
	}
	if len(infoAfterRes.Approvers) != 2 || infoAfterRes.Approvers[0] != "100" || infoAfterRes.Approvers[1] != "200" {
		t.Errorf("unexpected Approvers: %+v", infoAfterRes.Approvers)
	}

	// 4. policy_add_rule over socket: exact whitelist then verify enforcement
	addBytes := sendAndRecv(`{"policy_add_rule":{"target":"whitelist","match_type":"exact","pattern":["/bin/true"]}}`)
	var addRes protocol.PolicyAddRuleResponse
	if err := json.Unmarshal(addBytes, &addRes); err != nil {
		t.Fatalf("unmarshal policy_add_rule failed: %v", err)
	}
	if !addRes.Success {
		t.Fatalf("policy_add_rule failed: %s", addRes.Error)
	}
	badBytes := sendAndRecv(`{"policy_add_rule":{"target":"nope","match_type":"exact","pattern":["/bin/x"]}}`)
	var badRes protocol.PolicyAddRuleResponse
	if err := json.Unmarshal(badBytes, &badRes); err != nil {
		t.Fatalf("unmarshal bad policy_add_rule failed: %v", err)
	}
	if badRes.Success {
		t.Error("bad target must fail")
	}

	// 5. Clean up: unconfigure to terminate background poll loop
	_ = sendAndRecv(`{"telegram_set_config":{"bot_token":""}}`)
}

// TestDaemonAuditSocketRoundTrip exercises the audit op end to end over the
// unix socket: seed an audit event, query it back with a bounded page.
func TestDaemonAuditSocketRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "audit.sock")
	t.Setenv("TWOFADO_USER_CONFIG", filepath.Join(tmpDir, "config.json"))

	cfg := config.Conf{
		Socket:   sockPath,
		StateDir: tmpDir,
		Timeout:  5,
		Policy:   filepath.Join(tmpDir, "policy.json"),
	}
	svc := service.New(cfg)
	svc.Store.Append(protocol.AuditEvent{Ev: "request", UID: 1000, RID: "audit-rid-1"})
	svc.Store.Append(protocol.AuditEvent{Ev: "exec", UID: 1000, RID: "audit-rid-2"})
	go func() {
		_ = Serve(svc)
	}()
	for i := 0; i < 50; i++ {
		if conn, err := net.Dial("unix", sockPath); err == nil {
			conn.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	c, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer c.Close()
	if _, err := c.Write([]byte(`{"audit":{"limit":1}}` + "\n")); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	line, err := bufio.NewReader(c).ReadBytes('\n')
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	var res protocol.AuditQueryResponse
	if err := json.Unmarshal(line, &res); err != nil {
		t.Fatalf("unmarshal audit response: %v (%s)", err, line)
	}
	if len(res.Items) != 1 || res.Items[0].Ev != "exec" || res.Items[0].RID != "audit-rid-2" {
		t.Fatalf("audit page = %+v", res.Items)
	}
	if res.NextCursor == "" {
		t.Fatal("expected next_cursor with an older event outstanding")
	}

	// Resume the cursor to fetch the next (older) event.
	c2, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer c2.Close()
	req := fmt.Sprintf(`{"audit":{"limit":1,"cursor":"%s"}}`+"\n", res.NextCursor)
	if _, err := c2.Write([]byte(req)); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	line2, err := bufio.NewReader(c2).ReadBytes('\n')
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	var res2 protocol.AuditQueryResponse
	if err := json.Unmarshal(line2, &res2); err != nil {
		t.Fatalf("unmarshal audit page 2: %v (%s)", err, line2)
	}
	if len(res2.Items) != 1 || res2.Items[0].RID != "audit-rid-1" {
		t.Fatalf("audit page 2 = %+v", res2.Items)
	}

	// 3. Verify the client.Audit consumer helper works over the socket.
	if exitCode := client.Audit(sockPath, protocol.AuditQueryRequest{Limit: 1}); exitCode != 0 {
		t.Fatalf("client.Audit returned %d, want 0", exitCode)
	}
}

// TestDaemonAskSocketRoundTrip exercises the ask op end to end over the
// unix socket: the blocking call resolves once a selection is recorded.
func TestDaemonAskSocketRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "ask.sock")
	t.Setenv("TWOFADO_USER_CONFIG", filepath.Join(tmpDir, "config.json"))

	cfg := config.Conf{
		Socket:   sockPath,
		StateDir: tmpDir,
		Timeout:  5,
		Policy:   filepath.Join(tmpDir, "policy.json"),
	}
	svc := service.New(cfg)
	go func() {
		_ = Serve(svc)
	}()
	for i := 0; i < 50; i++ {
		if conn, err := net.Dial("unix", sockPath); err == nil {
			conn.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	type reply struct {
		raw []byte
		err error
	}
	got := make(chan reply, 1)
	go func() {
		c, err := net.Dial("unix", sockPath)
		if err != nil {
			got <- reply{err: err}
			return
		}
		defer c.Close()
		req := `{"ask":{"question":"pick one","options":["a","b","c"],"ttl_seconds":30}}`
		if _, err := c.Write([]byte(req + "\n")); err != nil {
			got <- reply{err: err}
			return
		}
		line, err := bufio.NewReader(c).ReadBytes('\n')
		got <- reply{raw: line, err: err}
	}()

	rid := ""
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && rid == "" {
		if items := svc.List().Items; len(items) == 1 && items[0].Kind == "ask" {
			rid = items[0].ID
		} else {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if rid == "" {
		t.Fatal("ask petition never appeared over the socket")
	}
	if !svc.Store.Select(rid, "b", 1, "telegram:42") {
		t.Fatal("selection rejected")
	}
	select {
	case r := <-got:
		if r.err != nil {
			t.Fatalf("socket read: %v", r.err)
		}
		var res protocol.AskResult
		if err := json.Unmarshal(r.raw, &res); err != nil {
			t.Fatalf("unmarshal ask result: %v (%s)", err, r.raw)
		}
		if res.Status != "selected" || res.Selection != "b" || res.SelectionIdx != 1 || res.ID != rid {
			t.Fatalf("ask result = %+v", res)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ask socket call did not return after selection")
	}
}

// TestDaemonCancelSocketRoundTrip exercises the cancel op end to end over the
// unix socket for pending exec petitions and ask petitions.
func TestDaemonCancelSocketRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "cancel.sock")
	t.Setenv("TWOFADO_USER_CONFIG", filepath.Join(tmpDir, "config.json"))

	cfg := config.Conf{
		Socket:   sockPath,
		StateDir: tmpDir,
		Timeout:  5,
		Policy:   filepath.Join(tmpDir, "policy.json"),
	}
	svc := service.New(cfg)
	go func() {
		_ = Serve(svc)
	}()
	for i := 0; i < 50; i++ {
		if conn, err := net.Dial("unix", sockPath); err == nil {
			conn.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	sendAndRecv := func(req string) []byte {
		c, err := net.Dial("unix", sockPath)
		if err != nil {
			t.Fatalf("dial failed: %v", err)
		}
		defer c.Close()
		if _, err := c.Write([]byte(req + "\n")); err != nil {
			t.Fatalf("write failed: %v", err)
		}
		line, err := bufio.NewReader(c).ReadBytes('\n')
		if err != nil {
			t.Fatalf("read failed: %v", err)
		}
		return line
	}

	// 1. Submit an ask request asynchronously
	type reply struct {
		raw []byte
		err error
	}
	got := make(chan reply, 1)
	go func() {
		c, err := net.Dial("unix", sockPath)
		if err != nil {
			got <- reply{err: err}
			return
		}
		defer c.Close()
		req := `{"ask":{"question":"cancel me","options":["yes","no"],"ttl_seconds":30}}`
		if _, err := c.Write([]byte(req + "\n")); err != nil {
			got <- reply{err: err}
			return
		}
		line, err := bufio.NewReader(c).ReadBytes('\n')
		got <- reply{raw: line, err: err}
	}()

	rid := ""
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && rid == "" {
		if items := svc.List().Items; len(items) == 1 && items[0].Kind == "ask" {
			rid = items[0].ID
		} else {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if rid == "" {
		t.Fatal("ask petition never appeared over the socket")
	}

	// 2. Send cancel request over socket
	cancelReq := fmt.Sprintf(`{"cancel":{"id":"%s","reason":"user aborted","by":"test"}}`, rid)
	cancelBytes := sendAndRecv(cancelReq)
	var cancelRes protocol.CancelResponse
	if err := json.Unmarshal(cancelBytes, &cancelRes); err != nil {
		t.Fatalf("unmarshal cancel response: %v (%s)", err, cancelBytes)
	}
	if !cancelRes.Cancelled {
		t.Fatalf("expected cancel success, got %+v", cancelRes)
	}

	// 3. Verify original ask client unblocked with denied/cancelled
	select {
	case r := <-got:
		if r.err != nil {
			t.Fatalf("socket read: %v", r.err)
		}
		var askRes protocol.AskResult
		if err := json.Unmarshal(r.raw, &askRes); err != nil {
			t.Fatalf("unmarshal ask result: %v (%s)", err, r.raw)
		}
		if askRes.Status != "denied" || askRes.Reason != "cancelled" {
			t.Fatalf("ask result = %+v, want denied/cancelled", askRes)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ask socket call did not unblock after cancel")
	}

	// 4. Verify client.Cancel helper works over the socket
	exitCode := client.Cancel(sockPath, rid, "idempotent cancel")
	if exitCode != 0 {
		t.Fatalf("client.Cancel returned %d, want 0", exitCode)
	}
}
