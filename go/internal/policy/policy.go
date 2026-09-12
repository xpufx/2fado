// Package policy evaluates argv against exact-match tiers.
package policy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
		if matchRule(argv, a) {
			return "allow"
		}
	}
	if p.Mode == "whitelist" {
		return "deny"
	}
	for _, d := range p.Blacklist {
		if matchRule(argv, d) {
			return "deny"
		}
	}
	return p.Default
}

// matchRule reports whether argv matches a stored rule vector.
// Single-element vectors are base-binary matches (argv[0] equal).
// Vectors containing "*" use single-arg wildcard matching ("*" matches
// exactly one arg; trailing "*" matches any suffix). All other vectors
// require exact argv equality.
func matchRule(argv, rule []string) bool {
	if len(rule) == 0 || len(argv) == 0 {
		return false
	}
	hasWild := false
	for _, e := range rule {
		if e == "*" {
			hasWild = true
			break
		}
	}
	if hasWild {
		return matchWild(argv, rule)
	}
	if len(rule) == 1 {
		return argv[0] == rule[0]
	}
	return equal(argv, rule)
}

func matchWild(argv, rule []string) bool {
	if len(rule) > 0 && rule[len(rule)-1] == "*" && len(rule) == 2 && rule[0] == "*" {
		return true
	}
	if rule[len(rule)-1] == "*" {
		prefix := rule[:len(rule)-1]
		if len(argv) < len(prefix) {
			return false
		}
		for i := range prefix {
			if prefix[i] == "*" {
				continue
			}
			if argv[i] != prefix[i] {
				return false
			}
		}
		return true
	}
	if len(argv) != len(rule) {
		return false
	}
	for i := range rule {
		if rule[i] == "*" {
			continue
		}
		if argv[i] != rule[i] {
			return false
		}
	}
	return true
}

// AddRule atomically appends pattern to target ("whitelist"|"blacklist")
// in the policy file at path, preserving 0600 permissions, and returns
// the reloaded policy. matchType is one of "exact"|"base"|"custom".
func AddRule(path, target, matchType string, pattern []string) (Policy, int, error) {
	if target != "whitelist" && target != "blacklist" {
		return Policy{}, 0, fmt.Errorf("bad target %q: want whitelist|blacklist", target)
	}
	if len(pattern) == 0 {
		return Policy{}, 0, fmt.Errorf("empty pattern")
	}
	for _, e := range pattern {
		if e == "" {
			return Policy{}, 0, fmt.Errorf("empty pattern element")
		}
	}
	switch matchType {
	case "exact":
		for _, e := range pattern {
			if e == "*" {
				return Policy{}, 0, fmt.Errorf("exact pattern must not contain wildcards")
			}
		}
	case "base":
		if len(pattern) != 1 || pattern[0] == "*" {
			return Policy{}, 0, fmt.Errorf("base pattern must be [baseBinary]")
		}
	case "custom":
	default:
		return Policy{}, 0, fmt.Errorf("bad match_type %q: want exact|base|custom", matchType)
	}
	p := Load(path)
	vec := append([]string(nil), pattern...)
	if target == "whitelist" {
		for _, r := range p.Whitelist {
			if equal(r, vec) {
				return p, len(p.Whitelist), nil
			}
		}
		p.Whitelist = append(p.Whitelist, vec)
	} else {
		for _, r := range p.Blacklist {
			if equal(r, vec) {
				return p, len(p.Blacklist), nil
			}
		}
		p.Blacklist = append(p.Blacklist, vec)
	}
	if err := saveAtomic(path, p); err != nil {
		return Policy{}, 0, err
	}
	p = Load(path)
	if target == "whitelist" {
		return p, len(p.Whitelist), nil
	}
	return p, len(p.Blacklist), nil
}

func saveAtomic(path string, p Policy) error {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	mode := os.FileMode(0o600)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".policy-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
