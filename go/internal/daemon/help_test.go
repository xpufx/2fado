package daemon

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"2fado/internal/protocol"
)

func TestHelpOpListMatchesClientMessage(t *testing.T) {
	ops, ok := protocol.Describe("")
	if !ok {
		t.Fatal("Describe(\"\") not ok")
	}
	ct := reflect.TypeFor[protocol.ClientMessage]()
	if len(ops) != ct.NumField() {
		t.Fatalf("op count %d != ClientMessage fields %d (drift)", len(ops), ct.NumField())
	}
	seen := map[string]bool{}
	for i, op := range ops {
		f := ct.Field(i)
		key := strings.SplitN(f.Tag.Get("json"), ",", 2)[0]
		if op.Op != key {
			t.Fatalf("op %d = %q, want envelope key %q", i, op.Op, key)
		}
		rt := f.Type
		for rt.Kind() == reflect.Pointer {
			rt = rt.Elem()
		}
		if op.Request != rt.Name() {
			t.Fatalf("op %q request %q, want %q", key, op.Request, rt.Name())
		}
		if op.Response == "" {
			t.Fatalf("op %q has no response type", key)
		}
		if len(op.RequestFields) == 0 && rt.NumField() > 0 {
			t.Fatalf("op %q missing request fields", key)
		}
		if len(op.ResponseFields) == 0 {
			t.Fatalf("op %q missing response fields", key)
		}
		seen[key] = true
	}
	for _, k := range protocol.OpKeys() {
		if !seen[k] {
			t.Fatalf("OpKeys %q not in Describe output", k)
		}
	}
}

func TestHelpNamedOpAndUnknownRequestGuide(t *testing.T) {
	ops, ok := protocol.Describe("run")
	if !ok || len(ops) != 1 || ops[0].Op != "run" {
		t.Fatalf("Describe(run) = %+v, %v", ops, ok)
	}
	if ops[0].Request != "RunRequest" || ops[0].Response != "RunResult" {
		t.Fatalf("run shapes = %+v", ops[0])
	}
	if _, ok := protocol.Describe("nope"); ok {
		t.Fatal("Describe(nonexistent) should fail")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(`{"bogus":{}}`), &raw); err != nil {
		t.Fatal(err)
	}
	var msg protocol.ClientMessage
	_ = json.Unmarshal([]byte(`{"bogus":{}}`), &msg)
	v := reflect.ValueOf(msg)
	matched := false
	for i := 0; i < v.NumField(); i++ {
		if !v.Field(i).IsNil() {
			matched = true
		}
	}
	if matched {
		t.Fatal("bogus envelope should match nothing")
	}
	guide := "unknown-request: valid ops: " + strings.Join(protocol.OpKeys(), ",")
	for _, k := range protocol.OpKeys() {
		if !strings.Contains(guide, k) {
			t.Fatalf("guide missing op %q", k)
		}
	}
}
