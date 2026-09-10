// Package policy evaluates argv against exact-match tiers.
package policy

import (
	"encoding/json"
	"os"
)

type Policy struct {
	Allow   [][]string `json:"allow"`
	Deny    [][]string `json:"deny"`
	Confirm [][]string `json:"confirm"`
	Default string     `json:"default"`
	EnvKeep []string   `json:"env_keep"`
	Target  string     `json:"target_user"`
}

func Load(path string) Policy {
	p := Policy{Default: "ask"}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &p)
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
	for _, d := range p.Deny {
		if equal(argv, d) {
			return "deny"
		}
	}
	for _, a := range p.Allow {
		if equal(argv, a) {
			return "allow"
		}
	}
	return p.Default
}
