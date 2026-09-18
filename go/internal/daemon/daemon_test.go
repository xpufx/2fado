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
