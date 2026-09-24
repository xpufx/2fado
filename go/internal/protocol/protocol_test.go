package protocol_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"2fado/internal/protocol"
)

// TestClientMessageOperationInventory verifies that ClientMessage defines exactly
// 16 operations including ask, select, cancel, and audit, that OpKeys() matches, and that all ops describe cleanly.
func TestClientMessageOperationInventory(t *testing.T) {
	const expectedOpCount = 16
	keys := protocol.OpKeys()
	if len(keys) != expectedOpCount {
		t.Fatalf("expected %d operations in OpKeys(), got %d: %v", expectedOpCount, len(keys), keys)
	}

	ct := reflect.TypeFor[protocol.ClientMessage]()
	if ct.NumField() != expectedOpCount {
		t.Fatalf("expected %d fields in ClientMessage, got %d", expectedOpCount, ct.NumField())
	}

	hasAsk := false
	hasSelect := false
	hasCancel := false
	hasAudit := false
	for _, k := range keys {
		if k == "ask" {
			hasAsk = true
		}
		if k == "select" {
			hasSelect = true
		}
		if k == "cancel" {
			hasCancel = true
		}
		if k == "audit" {
			hasAudit = true
		}
	}
	if !hasAsk {
		t.Fatalf("expected 'ask' operation in OpKeys(), found %v", keys)
	}
	if !hasSelect {
		t.Fatalf("expected 'select' operation in OpKeys(), found %v", keys)
	}
	if !hasCancel {
		t.Fatalf("expected 'cancel' operation in OpKeys(), found %v", keys)
	}
	if !hasAudit {
		t.Fatalf("expected 'audit' operation in OpKeys(), found %v", keys)
	}

	ops, ok := protocol.Describe("")
	if !ok {
		t.Fatal("protocol.Describe(\"\") failed")
	}
	if len(ops) != expectedOpCount {
		t.Fatalf("protocol.Describe(\"\") returned %d ops, want %d", len(ops), expectedOpCount)
	}

	askOps, ok := protocol.Describe("ask")
	if !ok || len(askOps) != 1 {
		t.Fatalf("protocol.Describe(\"ask\") failed or returned unexpected count: %v", askOps)
	}
	if askOps[0].Request != "AskRequest" || askOps[0].Response != "AskResult" {
		t.Fatalf("unexpected shapes for ask op: req=%s, resp=%s", askOps[0].Request, askOps[0].Response)
	}

	selectOps, ok := protocol.Describe("select")
	if !ok || len(selectOps) != 1 {
		t.Fatalf("protocol.Describe(\"select\") failed or returned unexpected count: %v", selectOps)
	}
	if selectOps[0].Request != "SelectRequest" || selectOps[0].Response != "SelectResponse" {
		t.Fatalf("unexpected shapes for select op: req=%s, resp=%s", selectOps[0].Request, selectOps[0].Response)
	}

	cancelOps, ok := protocol.Describe("cancel")
	if !ok || len(cancelOps) != 1 {
		t.Fatalf("protocol.Describe(\"cancel\") failed or returned unexpected count: %v", cancelOps)
	}
	if cancelOps[0].Request != "CancelRequest" || cancelOps[0].Response != "CancelResponse" {
		t.Fatalf("unexpected shapes for cancel op: req=%s, resp=%s", cancelOps[0].Request, cancelOps[0].Response)
	}

	auditOps, ok := protocol.Describe("audit")
	if !ok || len(auditOps) != 1 {
		t.Fatalf("protocol.Describe(\"audit\") failed or returned unexpected count: %v", auditOps)
	}
	if auditOps[0].Request != "AuditQueryRequest" || auditOps[0].Response != "AuditQueryResponse" {
		t.Fatalf("unexpected shapes for audit op: req=%s, resp=%s", auditOps[0].Request, auditOps[0].Response)
	}
}

// findRepoRoot walks upward from the current working directory to locate repo root containing go.mod and docs/.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current working dir: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "docs")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "go", "go.mod")); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root containing docs/ and go/go.mod")
		}
		dir = parent
	}
}

// TestDocsConsistency verifies that docs/backend-api.md, docs/backend-cli.md, and
// docs/backend-adapter.md cannot drift from the 15 ClientMessage operations in protocol.go.
func TestDocsConsistency(t *testing.T) {
	root := findRepoRoot(t)
	docFiles := []string{
		filepath.Join(root, "docs", "backend-api.md"),
		filepath.Join(root, "docs", "backend-cli.md"),
		filepath.Join(root, "docs", "backend-adapter.md"),
	}

	for _, path := range docFiles {
		contentBytes, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("failed to read doc file %s: %v", path, err)
		}
		content := string(contentBytes)
		relPath, _ := filepath.Rel(root, path)

		// 1. Ensure no stale "12 op", "13 op", "14 op", or "15 op" references exist.
		if strings.Contains(content, "12 op") || strings.Contains(content, "12 ops") || strings.Contains(content, "8 of 12") ||
			strings.Contains(content, "13 op") || strings.Contains(content, "13 ops") || strings.Contains(content, "8 of 13") ||
			strings.Contains(content, "14 op") || strings.Contains(content, "14 ops") || strings.Contains(content, "9 of 14") ||
			strings.Contains(content, "15 op") || strings.Contains(content, "15 ops") || strings.Contains(content, "10 of 15") {
			t.Errorf("%s still contains stale '12 op', '13 op', '14 op', or '15 op' reference", relPath)
		}
		if !strings.Contains(content, "16 ops") {
			t.Errorf("%s does not reference '16 ops'", relPath)
		}
	}

	keys := protocol.OpKeys()

	// Check backend-api.md and backend-adapter.md inventory lines contain all 16 keys.
	for _, docName := range []string{"backend-api.md", "backend-adapter.md"} {
		contentBytes, err := os.ReadFile(filepath.Join(root, "docs", docName))
		if err != nil {
			t.Fatalf("failed to read %s: %v", docName, err)
		}
		content := string(contentBytes)
		for _, k := range keys {
			if !strings.Contains(content, k) {
				t.Errorf("%s is missing operation %q in documentation", docName, k)
			}
		}
	}

	// Check backend-cli.md specific representations.
	cliDocBytes, err := os.ReadFile(filepath.Join(root, "docs", "backend-cli.md"))
	if err != nil {
		t.Fatalf("failed to read backend-cli.md: %v", err)
	}
	cliDoc := string(cliDocBytes)

	if !strings.Contains(cliDoc, "2fado ask --question") {
		t.Errorf("backend-cli.md missing '2fado ask --question' subcommand documentation")
	}
	if !strings.Contains(cliDoc, "2fado select <id>") {
		t.Errorf("backend-cli.md missing '2fado select <id>' subcommand documentation")
	}
	if !strings.Contains(cliDoc, "2fado cancel <id>") {
		t.Errorf("backend-cli.md missing '2fado cancel <id>' subcommand documentation")
	}
	if !strings.Contains(cliDoc, "2fado audit") {
		t.Errorf("backend-cli.md missing '2fado audit' subcommand documentation")
	}
	if !strings.Contains(cliDoc, "- `ask`:") {
		t.Errorf("backend-cli.md missing exit-code convention for `ask`")
	}
	if !strings.Contains(cliDoc, "- `select`:") {
		t.Errorf("backend-cli.md missing exit-code convention for `select`")
	}
	if !strings.Contains(cliDoc, "- `cancel`:") {
		t.Errorf("backend-cli.md missing exit-code convention for `cancel`")
	}
	if !strings.Contains(cliDoc, "- `audit`:") {
		t.Errorf("backend-cli.md missing exit-code convention for `audit`")
	}
	if !strings.Contains(cliDoc, "run, verdict, cancel, notify, ask, select, ack, status, version, list, recent, audit") {
		t.Errorf("backend-cli.md missing full 12 CLI socket ops list including cancel, ask, select, and audit")
	}

	// Check backend-adapter.md specific representations.
	adapterDocBytes, err := os.ReadFile(filepath.Join(root, "docs", "backend-adapter.md"))
	if err != nil {
		t.Fatalf("failed to read backend-adapter.md: %v", err)
	}
	adapterDoc := string(adapterDocBytes)

	if !strings.Contains(adapterDoc, "11 of 16 ops") {
		t.Errorf("backend-adapter.md missing '11 of 16 ops' reference")
	}
	if !strings.Contains(adapterDoc, "`run`, `notify`, `ask`, `version`, `help`") {
		t.Errorf("backend-adapter.md missing 'run, notify, ask, version, help' list")
	}
	if !strings.Contains(adapterDoc, "{select:{id,selection,selection_idx}}") {
		t.Errorf("backend-adapter.md missing select socket op in op table")
	}
	if !strings.Contains(adapterDoc, "{cancel:{id,reason,by:\"consumer\"}}") {
		t.Errorf("backend-adapter.md missing cancel socket op in op table")
	}
	if !strings.Contains(adapterDoc, "{audit:{") {
		t.Errorf("backend-adapter.md missing audit socket op in op table")
	}
}
