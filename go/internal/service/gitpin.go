// Git tree pinning: TOCTOU mitigation for workspace drift between
// petition (T0) and approval (T1). The daemon snapshots HEAD, branch,
// and status porcelain when it pages the operator, then re-checks
// before executing. HEAD moves and branch switches always block;
// dirty-tree drift warns by default and blocks under a strict policy
// (whitelist mode or TWOFADO_GIT_STRICT). Non-repos and git failures
// degrade to no pin / no check, never to denial.
package service

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"

	"2fado/internal/protocol"
)

const (
	gitProbeTimeout = 3 * time.Second
	maxPorcelain    = 32 * 1024
)

// snapshotGit freezes the tree state of cwd. A pin with Present=false
// means cwd is not a git worktree; nil means git was unusable (missing
// binary, timeout, unreadable cwd) and callers must skip the check.
func snapshotGit(cwd string) *protocol.GitPin {
	if strings.TrimSpace(cwd) == "" {
		return nil
	}
	if _, err := os.Stat(cwd); err != nil {
		return nil
	}
	if _, err := exec.LookPath("git"); err != nil {
		return nil
	}
	head, ok := gitOut(cwd, "rev-parse", "HEAD")
	if !ok {
		return &protocol.GitPin{Present: false}
	}
	branch, ok := gitOut(cwd, "rev-parse", "--abbrev-ref", "HEAD")
	if !ok {
		return &protocol.GitPin{Present: false}
	}
	porcelain, ok := gitOut(cwd, "status", "--porcelain=v1", "--untracked-files=normal")
	if !ok {
		return nil
	}
	if len(porcelain) > maxPorcelain {
		porcelain = porcelain[:maxPorcelain] + "\n…[truncated]"
	}
	return &protocol.GitPin{
		Head:      strings.TrimSpace(head),
		Branch:    strings.TrimSpace(branch),
		Porcelain: porcelain,
		Clean:     strings.TrimSpace(porcelain) == "",
		Present:   true,
	}
}

func gitOut(cwd string, args ...string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), gitProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"--no-optional-locks", "-C", cwd}, args...)...)
	cmd.Env = []string{"PATH=" + safePath}
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return string(out), true
}

func shortHead(h string) string {
	h = strings.TrimSpace(h)
	if len(h) > 12 {
		return h[:12]
	}
	if h == "" {
		return "unknown"
	}
	return h
}

// gitStrict reports whether dirty-tree drift blocks instead of warning.
// Whitelist mode is strict by construction; otherwise the operator opts
// in via TWOFADO_GIT_STRICT (1/true/strict/block).
func (s Service) gitStrict() bool {
	if s.Policy.Mode == "whitelist" {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("TWOFADO_GIT_STRICT"))) {
	case "1", "true", "yes", "strict", "block":
		return true
	}
	return false
}

// checkGitDrift compares the T0 pin stored under rid against the live
// tree. It returns nil when there is nothing to check (no pin, non-repo
// at T0, or git unusable at T1) so execution proceeds unaffected.
func (s Service) checkGitDrift(rid, cwd string) *protocol.GitDrift {
	rec, err := s.Store.Load(rid)
	if err != nil || rec.Git == nil || !rec.Git.Present {
		return nil
	}
	dir := rec.Cwd
	if dir == "" {
		dir = cwd
	}
	live := snapshotGit(dir)
	if live == nil {
		return nil
	}
	pinned := rec.Git
	if !live.Present {
		return &protocol.GitDrift{Drift: true, Block: true,
			Reason: "git worktree at " + dir + " is no longer a git repo"}
	}
	if live.Head != pinned.Head {
		return &protocol.GitDrift{Drift: true, Block: true,
			Reason: "HEAD moved " + shortHead(pinned.Head) + " -> " + shortHead(live.Head)}
	}
	if live.Branch != pinned.Branch {
		return &protocol.GitDrift{Drift: true, Block: true,
			Reason: "branch switch " + pinned.Branch + " -> " + live.Branch}
	}
	if live.Porcelain != pinned.Porcelain {
		reason := "DIRTY WORKSPACE: tree state changed since petition"
		switch {
		case pinned.Clean && !live.Clean:
			reason = "DIRTY WORKSPACE: clean tree at petition is now dirty"
		case !pinned.Clean && live.Clean:
			reason = "DIRTY WORKSPACE: dirty tree at petition is now clean"
		}
		return &protocol.GitDrift{Drift: true, Block: s.gitStrict(), Reason: reason}
	}
	return nil
}
