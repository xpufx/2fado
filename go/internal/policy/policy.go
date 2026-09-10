// Package policy evaluates argv against exact-match tiers.
package policy

import (
	"encoding/json"
	"os"
)

// Modes: "blacklist" (whitelist runs, blacklist denies, rest falls to
// Default — today's behavior) or "whitelist" (whitelist runs, everything
// else denies; blacklist and Default are documented noops).
type Policy struct {
	Mode      string     `json:"mode"`
	Whitelist [][]string `json:"whitelist"`
	Blacklist [][]string `json:"blacklist"`
	Confirm   [][]string `json:"confirm"`
	Default   string     `json:"default"`
	EnvKeep   []string   `json:"env_keep"`
	Target    string     `json:"target_user"`
}

func Load(path string) Policy {
	p := Policy{Mode: "blacklist", Default: "ask"}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &p)
	}
	if p.Mode == "" {
		p.Mode = "blacklist"
	}
	if p.Default == "" {
		p.Default = "ask"
	}
	return p
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// NeedsConfirm reports whether argv is opted into the two-step
// challenge. Exact matches only — heuristics may never decide.
func (p Policy) NeedsConfirm(argv []string) bool {
	for _, c := range p.Confirm {
		if equal(argv, c) {
			return true
		}
	}
	return false
}

func (p Policy) Tier(argv []string) string {
	for _, a := range p.Whitelist {
		if equal(argv, a) {
			return "allow"
		}
	}
	if p.Mode == "whitelist" {
		return "deny"
	}
	for _, d := range p.Blacklist {
		if equal(argv, d) {
			return "deny"
		}
	}
	return p.Default
}
