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
		out = append(out, protocol.PendingItem{
			ID: rid, Argv: rec.Argv, UID: rec.UID, Cwd: rec.Cwd,
			ExpiresIn: rec.Expires - now,
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
		out = append(out, protocol.RecentItem{
			ID: f.rid, Argv: rec.Argv, Cwd: rec.Cwd,
			Decision: v.Decision, By: v.By, Exit: exit, Output: output,
		})
	}
	if out == nil {
		out = []protocol.RecentItem{}
	}
	return out
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
