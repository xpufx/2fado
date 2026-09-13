// Package store owns the on-disk seams: pending requests, atomic
// verdicts, append-only audit. Files are authoritative; memory is cache.
package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"2fado/internal/protocol"
)

type Store struct {
	Dir     string
	Pending string
	Audit   string
	Offset  string
}

func New(dir string) Store {
	return Store{
		Dir:     dir,
		Pending: filepath.Join(dir, "pending"),
		Audit:   filepath.Join(dir, "audit.log"),
		Offset:  filepath.Join(dir, "tg_offset"),
	}
}

func (s Store) Init() error {
	return os.MkdirAll(s.Pending, 0o755)
}

func (s Store) pendingPath(rid string) string {
	return filepath.Join(s.Pending, rid+".json")
}

func (s Store) Save(rec protocol.PendingRecord, rid string) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	_ = os.MkdirAll(s.Pending, 0o755)
	return os.WriteFile(s.pendingPath(rid), data, 0o600)
}

func (s Store) Load(rid string) (protocol.PendingRecord, error) {
	var rec protocol.PendingRecord
	data, err := os.ReadFile(s.pendingPath(rid))
	if err != nil {
		return rec, err
	}
	err = json.Unmarshal(data, &rec)
	return rec, err
}

func (s Store) AttachPager(rid, chat string, msgID int64) {
	rec, err := s.Load(rid)
	if err != nil {
		return
	}
	rec.ChatID = chat
	rec.MsgID = msgID
	_ = s.Save(rec, rid)
}

// Consume writes the verdict exactly once (O_EXCL): one verdict wins.
func (s Store) Consume(rid, decision, by string) bool {
	data, err := json.Marshal(protocol.VerdictRecord{
		Decision: decision,
		By:       by,
		UnixNano: time.Now().UnixNano(),
	})
	if err != nil {
		return false
	}
	f, err := os.OpenFile(filepath.Join(s.Pending, rid+".verdict"),
		os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return false
	}
	defer f.Close()
	_, err = f.Write(data)
	return err == nil
}

// DefaultPruneTTL is the retention window for decided or expired records.
const DefaultPruneTTL = 24 * time.Hour

// Prune deletes decided (.verdict) or expired records older than the TTL,
// keeping live undecided, unexpired petitions. Returns the purge count.
func (s Store) Prune(olderThan time.Duration) (int, error) {
	entries, err := os.ReadDir(s.Pending)
	if err != nil {
		return 0, err
	}
	now := time.Now()
	seen := map[string]bool{}
	purged := 0
	for _, e := range entries {
		name := e.Name()
		var rid string
		switch {
		case strings.HasSuffix(name, ".json"):
			rid = strings.TrimSuffix(name, ".json")
		case strings.HasSuffix(name, ".verdict"), strings.HasSuffix(name, ".result"):
			dot := strings.LastIndex(name, ".")
			rid = name[:dot]
		default:
			continue
		}
		if seen[rid] {
			continue
		}
		seen[rid] = true
		rec, err := s.Load(rid)
		if err == nil && s.Verdict(rid) == nil && rec.Expires > now.Unix() {
			continue
		}
		oldest := now
		for _, suf := range []string{".json", ".verdict", ".result"} {
			if fi, err := os.Stat(filepath.Join(s.Pending, rid+suf)); err == nil {
				if fi.ModTime().Before(oldest) {
					oldest = fi.ModTime()
				}
			}
		}
		if now.Sub(oldest) > olderThan {
			_ = s.Purge(rid)
			purged++
		}
	}
	return purged, nil
}

// Purge deletes all on-disk artifacts for a request ID.
func (s Store) Purge(rid string) error {
	_ = os.Remove(s.pendingPath(rid))
	_ = os.Remove(filepath.Join(s.Pending, rid+".verdict"))
	_ = os.Remove(filepath.Join(s.Pending, rid+".result"))
	return nil
}

// FindConfirmation returns the request ID and record of a child confirmation petition, if any.
func (s Store) FindConfirmation(parentRID string) (string, *protocol.PendingRecord) {
	entries, err := os.ReadDir(s.Pending)
	if err != nil {
		return "", nil
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		rid := strings.TrimSuffix(name, ".json")
		rec, err := s.Load(rid)
		if err != nil {
			continue
		}
		if rec.ConfirmOf == parentRID {
			return rid, &rec
		}
	}
	return "", nil
}

// List returns unexpired, undecided records, newest last.
func (s Store) List(now int64) []protocol.PendingItem {
	entries, err := os.ReadDir(s.Pending)
	if err != nil {
		return nil
	}
	var out []protocol.PendingItem
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		rid := strings.TrimSuffix(name, ".json")
		if s.Verdict(rid) != nil {
			continue
		}
		rec, err := s.Load(rid)
		if err != nil || rec.Expires <= now {
			continue
		}
		step := rec.Step
		if step == "" {
			step = "initial"
		}
		out = append(out, protocol.PendingItem{
			ID:        rid,
			Argv:      rec.Argv,
			UID:       rec.UID,
			Cwd:       rec.Cwd,
			ExpiresIn: rec.Expires - now,
			Step:      step,
			ConfirmOf: rec.ConfirmOf,
			Preview:   rec.Preview,
		})
	}
	return out
}

func (s Store) Verdict(rid string) *protocol.VerdictRecord {
	data, err := os.ReadFile(filepath.Join(s.Pending, rid+".verdict"))
	if err != nil {
		return nil
	}
	var v protocol.VerdictRecord
	if err := json.Unmarshal(data, &v); err != nil {
		return nil
	}
	return &v
}

// ResultRecord persists an execution outcome next to its verdict so the
// plugin can show command + exit + output after the fact.
func (s Store) SaveResult(rid string, exit int, output string) {
	if len(output) > 8192 {
		output = output[:8192] + "\n…[truncated]"
	}
	data, err := json.Marshal(protocol.RecentItem{ID: rid, Exit: exit, Output: output})
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(s.Pending, rid+".result"), data, 0o600)
}

func (s Store) loadResult(rid string) (int, string) {
	data, err := os.ReadFile(filepath.Join(s.Pending, rid+".result"))
	if err != nil {
		return -1, ""
	}
	var r protocol.RecentItem
	if err := json.Unmarshal(data, &r); err != nil {
		return -1, ""
	}
	return r.Exit, r.Output
}

// Recent returns decided records, newest first, capped at limit.
func (s Store) Recent(limit int) []protocol.RecentItem {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	entries, err := os.ReadDir(s.Pending)
	if err != nil {
		return nil
	}
	type decided struct {
		rid string
		mod time.Time
	}
	var found []decided
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".verdict") {
			continue
		}
		rid := strings.TrimSuffix(name, ".verdict")
		info, err := e.Info()
		if err != nil {
			continue
		}
		found = append(found, decided{rid: rid, mod: info.ModTime()})
	}
	sort.Slice(found, func(i, j int) bool { return found[i].mod.After(found[j].mod) })
	if len(found) > limit {
		found = found[:limit]
	}
	var out []protocol.RecentItem
	for _, f := range found {
		rec, err := s.Load(f.rid)
		if err != nil {
			continue
		}
		v := s.Verdict(f.rid)
		if v == nil {
			continue
		}
		exit, output := s.loadResult(f.rid)
		step := rec.Step
		if step == "" {
			step = "initial"
		}
		dec := v.Decision
		if exit == -1 {
			if output == "confirmation_timeout" {
				dec = "timeout"
			} else if output == "client_aborted" {
				dec = "client_aborted"
			} else if childRID, _ := s.FindConfirmation(f.rid); childRID != "" {
				if cv := s.Verdict(childRID); cv != nil {
					dec = cv.Decision
				}
			}
		}
		out = append(out, protocol.RecentItem{
			ID:        f.rid,
			Argv:      rec.Argv,
			Cwd:       rec.Cwd,
			Decision:  dec,
			By:        v.By,
			Exit:      exit,
			Output:    output,
			Step:      step,
			ConfirmOf: rec.ConfirmOf,
		})
	}
	if out == nil {
		out = []protocol.RecentItem{}
	}
	return out
}

// Status returns the current state and result (if available) for a specific request ID.
func (s Store) Status(rid string, now int64) protocol.StatusResponse {
	rec, err := s.Load(rid)
	if err != nil {
		return protocol.StatusResponse{ID: rid, Status: "not_found", Exit: -1}
	}
	step := rec.Step
	if step == "" {
		step = "initial"
	}
	v := s.Verdict(rid)
	if v == nil {
		if rec.Expires > now {
			return protocol.StatusResponse{
				ID:        rid,
				Status:    "pending",
				Argv:      rec.Argv,
				Cwd:       rec.Cwd,
				UID:       rec.UID,
				ExpiresIn: rec.Expires - now,
				Exit:      -1,
				Step:      step,
				ConfirmOf: rec.ConfirmOf,
				Preview:   rec.Preview,
			}
		}
		return protocol.StatusResponse{
			ID:        rid,
			Status:    "timeout",
			Argv:      rec.Argv,
			Cwd:       rec.Cwd,
			UID:       rec.UID,
			Exit:      -1,
			Step:      step,
			ConfirmOf: rec.ConfirmOf,
			Preview:   rec.Preview,
		}
	}

	resultPath := filepath.Join(s.Pending, rid+".result")
	data, err := os.ReadFile(resultPath)
	if err != nil {
		// Verdict recorded, but result file not yet written
		if v.Decision == "approve" {
			// If this was an initial request, check if a child confirmation is awaiting verdict
			if step == "initial" {
				if childRID, childRec := s.FindConfirmation(rid); childRec != nil && s.Verdict(childRID) == nil {
					expiresIn := childRec.Expires - now
					if expiresIn < 0 {
						expiresIn = 0
					}
					return protocol.StatusResponse{
						ID:        rid,
						Status:    "confirming",
						Decision:  v.Decision,
						By:        v.By,
						Argv:      rec.Argv,
						Cwd:       rec.Cwd,
						UID:       rec.UID,
						ExpiresIn: expiresIn,
						Exit:      -1,
						Step:      step,
						ConfirmOf: rec.ConfirmOf,
						Preview:   rec.Preview,
					}
				}
			}
			return protocol.StatusResponse{
				ID:        rid,
				Status:    "running",
				Decision:  v.Decision,
				By:        v.By,
				Argv:      rec.Argv,
				Cwd:       rec.Cwd,
				UID:       rec.UID,
				Exit:      -1,
				Step:      step,
				ConfirmOf: rec.ConfirmOf,
				Preview:   rec.Preview,
			}
		}
		return protocol.StatusResponse{
			ID:        rid,
			Status:    v.Decision,
			Decision:  v.Decision,
			By:        v.By,
			Argv:      rec.Argv,
			Cwd:       rec.Cwd,
			UID:       rec.UID,
			Exit:      -1,
			Step:      step,
			ConfirmOf: rec.ConfirmOf,
			Preview:   rec.Preview,
		}
	}

	var r protocol.RecentItem
	if err := json.Unmarshal(data, &r); err != nil {
		r.Exit = -1
	}
	status := "completed"
	decision := v.Decision
	if v.Decision == "deny" {
		status = "denied"
	} else if v.Decision == "timeout" || v.Decision == "confirmation_timeout" || r.Output == "confirmation_timeout" {
		status = "timeout"
		if r.Output == "confirmation_timeout" {
			decision = "confirmation_timeout"
		}
	} else if v.Decision == "client_aborted" || r.Output == "client_aborted" {
		status = "client_aborted"
		if r.Output == "client_aborted" {
			decision = "client_aborted"
		}
	} else if r.Exit == -1 {
		if childRID, _ := s.FindConfirmation(rid); childRID != "" {
			if cv := s.Verdict(childRID); cv != nil {
				decision = cv.Decision
				if cv.Decision == "deny" {
					status = "denied"
				} else if cv.Decision == "timeout" || cv.Decision == "confirmation_timeout" {
					status = "timeout"
				} else if cv.Decision == "client_aborted" {
					status = "client_aborted"
				}
			}
		}
	}
	return protocol.StatusResponse{
		ID:        rid,
		Status:    status,
		Decision:  decision,
		By:        v.By,
		Argv:      rec.Argv,
		Cwd:       rec.Cwd,
		UID:       rec.UID,
		Exit:      r.Exit,
		Output:    r.Output,
		Step:      step,
		ConfirmOf: rec.ConfirmOf,
		Preview:   rec.Preview,
	}
}

func (s Store) Append(ev protocol.AuditEvent) {
	ev.TS = time.Now().UTC().Format(time.RFC3339)
	data, err := json.Marshal(ev)
	if err != nil {
		return
	}
	f, err := os.OpenFile(s.Audit, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(data, '\n'))
}

func (s Store) GetOffset() int64 {
	var n int64
	data, err := os.ReadFile(s.Offset)
	if err != nil {
		return 0
	}
	for _, ch := range string(data) {
		if ch < '0' || ch > '9' {
			return 0
		}
		n = n*10 + int64(ch-'0')
	}
	return n
}

func (s Store) SetOffset(n int64) {
	_ = os.WriteFile(s.Offset, []byte(itoa(n)), 0o600)
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
