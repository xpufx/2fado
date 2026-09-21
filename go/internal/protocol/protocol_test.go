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
// 13 operations including ask, that OpKeys() matches, and that all ops describe cleanly.
func TestClientMessageOperationInventory(t *testing.T) {
	const expectedOpCount = 13
	keys := protocol.OpKeys()
	if len(keys) != expectedOpCount {
		t.Fatalf("expected %d operations in OpKeys(), got %d: %v", expectedOpCount, len(keys), keys)
	}

	ct := reflect.TypeFor[protocol.ClientMessage]()
	if ct.NumField() != expectedOpCount {
		t.Fatalf("expected %d fields in ClientMessage, got %d", expectedOpCount, ct.NumField())
	}

	hasAsk := false
	for _, k := range keys {
		if k == "ask" {
			hasAsk = true
			break
		}
	}
	if !hasAsk {
		t.Fatalf("expected 'ask' operation in OpKeys(), found %v", keys)
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
// docs/backend-adapter.md cannot drift from the 13 ClientMessage operations in protocol.go.
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

		// 1. Ensure no stale "12 op" references exist.
		if strings.Contains(content, "12 op") || strings.Contains(content, "12 ops") || strings.Contains(content, "8 of 12") {
			t.Errorf("%s still contains stale '12 op' reference", relPath)
		}

		// 2. Ensure each doc explicitly references 13 ops.
		if !strings.Contains(content, "13 ops") {
			t.Errorf("%s does not reference '13 ops'", relPath)
		}
	}

	keys := protocol.OpKeys()

	// Check backend-api.md and backend-adapter.md inventory lines contain all 13 keys.
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
	if !strings.Contains(cliDoc, "- `ask`:") {
		t.Errorf("backend-cli.md missing exit-code convention for `ask`")
	}
	if !strings.Contains(cliDoc, "run, verdict, notify, ask, ack, status, version, list, recent") {
		t.Errorf("backend-cli.md missing full 9 CLI socket ops list including ask")
	}

	// Check backend-adapter.md specific representations.
	adapterDocBytes, err := os.ReadFile(filepath.Join(root, "docs", "backend-adapter.md"))
	if err != nil {
		t.Fatalf("failed to read backend-adapter.md: %v", err)
	}
	adapterDoc := string(adapterDocBytes)

	if !strings.Contains(adapterDoc, "8 of 13 ops") {
		t.Errorf("backend-adapter.md missing '8 of 13 ops' reference")
	}
	if !strings.Contains(adapterDoc, "`run`, `notify`, `ask`, `version`, `help`") {
		t.Errorf("backend-adapter.md missing 'run, notify, ask, version, help' list")
	}
}
