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
	if err := os.MkdirAll(s.Pending, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(s.Dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(s.Pending, 0o700); err != nil {
		return err
	}
	if err := validateAnchorDir(s.Dir); err != nil {
		return err
	}
	return validateAnchorDir(s.Pending)
}

func (s Store) pendingPath(rid string) string {
	return filepath.Join(s.Pending, rid+".json")
}

func (s Store) Save(rec protocol.PendingRecord, rid string) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	_ = os.MkdirAll(s.Pending, 0o700)
	return writeNoFollow(s.pendingPath(rid), data, 0o600, false)
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

// SetAuthURL records an interactive WebAuthn/2FA challenge URL detected
// during execution. First URL wins; later detections are ignored.
func (s Store) SetAuthURL(rid, authURL string) {
	if authURL == "" {
		return
	}
	rec, err := s.Load(rid)
	if err != nil || rec.AuthURL != "" {
		return
	}
	rec.AuthURL = authURL
	_ = s.Save(rec, rid)
}

// SetPageList records the title and backing lines of a paged Telegram
// list card so "page:<rid>:<n>" callbacks can re-render the card in
// place without a separate cursor. Paging is non-binding and idempotent.
func (s Store) SetPageList(rid, title string, lines []string) {
	rec, err := s.Load(rid)
	if err != nil {
		return
	}
	rec.Summary = title
	rec.PageLines = lines
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
		os.O_WRONLY|os.O_CREATE|os.O_EXCL|noFollow, 0o600)
	if err != nil {
		return false
	}
	defer f.Close()
	if _, err = f.Write(data); err != nil {
		return false
	}
	return f.Sync() == nil
}

// Ack writes a non-binding acknowledgement exactly once (O_EXCL): first
// ack wins. Only valid on existing notify-kind records; exec petitions
// and unknown IDs are rejected.
func (s Store) Ack(rid, by string) bool {
	rec, err := s.Load(rid)
	if err != nil || rec.Kind != "notify" {
		return false
	}
	data, err := json.Marshal(protocol.AckRecord{
		By:       by,
		UnixNano: time.Now().UnixNano(),
	})
	if err != nil {
		return false
	}
	f, err := os.OpenFile(filepath.Join(s.Pending, rid+".ack"),
		os.O_WRONLY|os.O_CREATE|os.O_EXCL|noFollow, 0o600)
	if err != nil {
		return false
	}
	defer f.Close()
	if _, err = f.Write(data); err != nil {
		return false
	}
	return f.Sync() == nil
}

// AckInfo returns the acknowledgement for a notify petition, if any.
func (s Store) AckInfo(rid string) *protocol.AckRecord {
	data, err := os.ReadFile(filepath.Join(s.Pending, rid+".ack"))
	if err != nil {
		return nil
	}
	var a protocol.AckRecord
	if err := json.Unmarshal(data, &a); err != nil {
		return nil
	}
	return &a
}

// Select writes an ask-petition option choice exactly once (O_EXCL): the
// first selection wins. Only valid on existing ask-kind records; other
// kinds and unknown IDs are rejected.
func (s Store) Select(rid, selection string, idx int, by string) bool {
	rec, err := s.Load(rid)
	if err != nil || rec.Kind != "ask" {
		return false
	}
	data, err := json.Marshal(protocol.SelectionRecord{
		Selection:    selection,
		SelectionIdx: idx,
		By:           by,
		UnixNano:     time.Now().UnixNano(),
	})
	if err != nil {
		return false
	}
	f, err := os.OpenFile(filepath.Join(s.Pending, rid+".selection"),
		os.O_WRONLY|os.O_CREATE|os.O_EXCL|noFollow, 0o600)
	if err != nil {
		return false
	}
	defer f.Close()
	if _, err = f.Write(data); err != nil {
		return false
	}
	return f.Sync() == nil
}

// SelectionInfo returns the recorded ask choice for a petition, if any.
func (s Store) SelectionInfo(rid string) *protocol.SelectionRecord {
	data, err := os.ReadFile(filepath.Join(s.Pending, rid+".selection"))
	if err != nil {
		return nil
	}
	var sel protocol.SelectionRecord
	if err := json.Unmarshal(data, &sel); err != nil {
		return nil
	}
	return &sel
}

// Cancel writes a cancellation record exactly once (O_EXCL): first cancel wins.
// Only valid on existing records; unknown IDs are rejected.
func (s Store) Cancel(rid, by, reason string) bool {
	if _, err := s.Load(rid); err != nil {
		return false
	}
	data, err := json.Marshal(protocol.CancelRecord{
		By:       by,
		Reason:   reason,
		UnixNano: time.Now().UnixNano(),
	})
	if err != nil {
		return false
	}
	f, err := os.OpenFile(filepath.Join(s.Pending, rid+".cancel"),
		os.O_WRONLY|os.O_CREATE|os.O_EXCL|noFollow, 0o600)
	if err != nil {
		return false
	}
	defer f.Close()
	if _, err = f.Write(data); err != nil {
		return false
	}
	return f.Sync() == nil
}

// CancelInfo returns the recorded cancellation for a petition, if any.
func (s Store) CancelInfo(rid string) *protocol.CancelRecord {
	data, err := os.ReadFile(filepath.Join(s.Pending, rid+".cancel"))
	if err != nil {
		return nil
	}
	var c protocol.CancelRecord
	if err := json.Unmarshal(data, &c); err != nil {
		return nil
	}
	return &c
}

// DefaultPruneTTL is the retention window for decided or expired records.
const DefaultPruneTTL = 30 * 24 * time.Hour

// Prune deletes decided (.verdict), acked (.ack), or expired records older
// than the TTL, keeping live undecided, unacked, unexpired petitions.
// Returns the purge count.
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
		case strings.HasSuffix(name, ".verdict"), strings.HasSuffix(name, ".result"), strings.HasSuffix(name, ".ack"), strings.HasSuffix(name, ".selection"), strings.HasSuffix(name, ".cancel"):
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
		if err == nil && s.Verdict(rid) == nil && s.AckInfo(rid) == nil && s.SelectionInfo(rid) == nil && s.CancelInfo(rid) == nil && rec.Expires > now.Unix() {
			continue
		}
		oldest := now
		for _, suf := range []string{".json", ".verdict", ".result", ".ack", ".selection", ".cancel"} {
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
	_ = os.Remove(filepath.Join(s.Pending, rid+".ack"))
	_ = os.Remove(filepath.Join(s.Pending, rid+".selection"))
	_ = os.Remove(filepath.Join(s.Pending, rid+".cancel"))
	_ = os.Remove(filepath.Join(s.Dir, "sealed", rid+".tar.gz"))
	_ = os.RemoveAll(filepath.Join(s.Dir, "sealed", rid+".d"))
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
		r := strings.TrimSuffix(name, ".json")
		rec, err := s.Load(r)
		if err != nil {
			continue
		}
		if rec.ConfirmOf == parentRID {
			return r, &rec
		}
	}
	return "", nil
}

// List returns unexpired, undecided, unacked records, newest last.
// Acked notify petitions clear early: they leave the pending queue but
// remain queryable via Status and Recent.
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
		if s.AckInfo(rid) != nil {
			continue
		}
		if s.SelectionInfo(rid) != nil {
			continue
		}
		if s.CancelInfo(rid) != nil {
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
			ID:           rid,
			Argv:         rec.Argv,
			UID:          rec.UID,
			AsUID:        rec.AsUID,
			Cwd:          rec.Cwd,
			ExpiresIn:    rec.Expires - now,
			Step:         step,
			ConfirmOf:    rec.ConfirmOf,
			Preview:      rec.Preview,
			AuthURL:      rec.AuthURL,
			Kind:         rec.Kind,
			Link:         rec.Link,
			Summary:      rec.Summary,
			Question:     rec.Question,
			Options:      rec.Options,
			Selection:    rec.Selection,
			SelectionIdx: rec.SelectionIdx,
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
	_ = writeNoFollow(filepath.Join(s.Pending, rid+".result"), data, 0o600, true)
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

// Recent returns decided or acked records, newest first, capped at limit.
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
		if !strings.HasSuffix(name, ".verdict") && !strings.HasSuffix(name, ".ack") && !strings.HasSuffix(name, ".selection") && !strings.HasSuffix(name, ".cancel") {
			continue
		}
		dot := strings.LastIndex(name, ".")
		rid := name[:dot]
		info, err := e.Info()
		if err != nil {
			continue
		}
		dup := false
		for _, f := range found {
			if f.rid == rid {
				dup = true
				break
			}
		}
		if dup {
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
		step := rec.Step
		if step == "" {
			step = "initial"
		}
		if cancel := s.CancelInfo(f.rid); cancel != nil {
			out = append(out, protocol.RecentItem{
				ID:           f.rid,
				Argv:         rec.Argv,
				Cwd:          rec.Cwd,
				AsUID:        rec.AsUID,
				Decision:     "cancelled",
				By:           cancel.By,
				Exit:         -1,
				Output:       cancel.Reason,
				Step:         step,
				ConfirmOf:    rec.ConfirmOf,
				AuthURL:      rec.AuthURL,
				Kind:         rec.Kind,
				Link:         rec.Link,
				Summary:      rec.Summary,
				Question:     rec.Question,
				Options:      rec.Options,
			})
			continue
		}
		if ack := s.AckInfo(f.rid); ack != nil {
			out = append(out, protocol.RecentItem{
				ID:       f.rid,
				Argv:     rec.Argv,
				Cwd:      rec.Cwd,
				AsUID:    rec.AsUID,
				Decision: "ack",
				By:       ack.By,
				Exit:     -1,
				Kind:     rec.Kind,
				Link:     rec.Link,
				Summary:  rec.Summary,
				Acked:    true,
				AckBy:    ack.By,
			})
			continue
		}
		v := s.Verdict(f.rid)
		if v == nil {
			if sel := s.SelectionInfo(f.rid); sel != nil {
				out = append(out, protocol.RecentItem{
					ID:           f.rid,
					Cwd:          rec.Cwd,
					AsUID:        rec.AsUID,
					Decision:     "select",
					By:           sel.By,
					Exit:         -1,
					Kind:         rec.Kind,
					Link:         rec.Link,
					Summary:      rec.Summary,
					Question:     rec.Question,
					Options:      rec.Options,
					Selection:    sel.Selection,
					SelectionIdx: sel.SelectionIdx,
				})
			}
			continue
		}
		exit, output := s.loadResult(f.rid)
		dec := v.Decision
		if exit == -1 {
			if output == "confirmation_timeout" {
				dec = "timeout"
			} else if output == "client_aborted" {
				dec = "client_aborted"
			} else if output == "cancelled" {
				dec = "cancelled"
			} else if childRID, _ := s.FindConfirmation(f.rid); childRID != "" {
				if s.CancelInfo(childRID) != nil {
					dec = "cancelled"
				} else if cv := s.Verdict(childRID); cv != nil {
					dec = cv.Decision
				}
			}
		}
		out = append(out, protocol.RecentItem{
			ID:        f.rid,
			Argv:      rec.Argv,
			Cwd:       rec.Cwd,
			AsUID:     rec.AsUID,
			Decision:  dec,
			By:        v.By,
			Exit:      exit,
			Output:    output,
			Step:      step,
			ConfirmOf: rec.ConfirmOf,
			AuthURL:   rec.AuthURL,
			Kind:      rec.Kind,
			Link:      rec.Link,
			Summary:   rec.Summary,
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
	if rec.Kind == "ask" {
		st := protocol.StatusResponse{
			ID: rid, Cwd: rec.Cwd, UID: rec.UID, AsUID: rec.AsUID, Exit: -1,
			Step: step, Kind: rec.Kind, Link: rec.Link, Summary: rec.Summary,
			Question: rec.Question, Options: rec.Options,
		}
		if cancel := s.CancelInfo(rid); cancel != nil {
			st.Status = "cancelled"
			st.Decision = "cancelled"
			st.By = cancel.By
			st.Output = cancel.Reason
			return st
		}
		if sel := s.SelectionInfo(rid); sel != nil {
			st.Status = "selected"
			st.Decision = "select"
			st.By = sel.By
			st.Selection = sel.Selection
			st.SelectionIdx = sel.SelectionIdx
			return st
		}
		if rec.Expires > now {
			st.Status = "pending"
			st.ExpiresIn = rec.Expires - now
			return st
		}
		st.Status = "timeout"
		return st
	}
	if rec.Kind == "notify" {
		if cancel := s.CancelInfo(rid); cancel != nil {
			return protocol.StatusResponse{
				ID:        rid,
				Status:    "cancelled",
				Decision:  "cancelled",
				By:        cancel.By,
				Output:    cancel.Reason,
				Cwd:       rec.Cwd,
				UID:       rec.UID,
				AsUID:     rec.AsUID,
				ExpiresIn: 0,
				Exit:      -1,
				Step:      step,
				Kind:      rec.Kind,
				Link:      rec.Link,
				Summary:   rec.Summary,
			}
		}
		if ack := s.AckInfo(rid); ack != nil {
			return protocol.StatusResponse{
				ID:        rid,
				Status:    "acked",
				Decision:  "ack",
				By:        ack.By,
				Cwd:       rec.Cwd,
				UID:       rec.UID,
				AsUID:     rec.AsUID,
				ExpiresIn: 0,
				Exit:      -1,
				Step:      step,
				Kind:      rec.Kind,
				Link:      rec.Link,
				Summary:   rec.Summary,
				Acked:     true,
				AckBy:     ack.By,
				AckAt:     ack.UnixNano,
			}
		}
		if rec.Expires > now {
			return protocol.StatusResponse{
				ID:        rid,
				Status:    "pending",
				Cwd:       rec.Cwd,
				UID:       rec.UID,
				AsUID:     rec.AsUID,
				ExpiresIn: rec.Expires - now,
				Exit:      -1,
				Step:      step,
				Kind:      rec.Kind,
				Link:      rec.Link,
				Summary:   rec.Summary,
			}
		}
		return protocol.StatusResponse{
			ID:      rid,
			Status:  "timeout",
			Cwd:     rec.Cwd,
			UID:     rec.UID,
			Exit:    -1,
			Step:    step,
			Kind:    rec.Kind,
			Link:    rec.Link,
			Summary: rec.Summary,
		}
	}
	if cancel := s.CancelInfo(rid); cancel != nil {
		return protocol.StatusResponse{
			ID:        rid,
			Status:    "cancelled",
			Decision:  "cancelled",
			By:        cancel.By,
			Output:    cancel.Reason,
			Argv:      rec.Argv,
			Cwd:       rec.Cwd,
			UID:       rec.UID,
			AsUID:     rec.AsUID,
			Exit:      -1,
			Step:      step,
			ConfirmOf: rec.ConfirmOf,
			Preview:   rec.Preview,
			AuthURL:   rec.AuthURL,
		}
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
				AsUID:     rec.AsUID,
				ExpiresIn: rec.Expires - now,
				Exit:      -1,
				Step:      step,
				ConfirmOf: rec.ConfirmOf,
				Preview:   rec.Preview,
				AuthURL:   rec.AuthURL,
			}
		}
		return protocol.StatusResponse{
			ID:        rid,
			Status:    "timeout",
			Argv:      rec.Argv,
			Cwd:       rec.Cwd,
			UID:       rec.UID,
			AsUID:     rec.AsUID,
			Exit:      -1,
			Step:      step,
			ConfirmOf: rec.ConfirmOf,
			Preview:   rec.Preview,
			AuthURL:   rec.AuthURL,
		}
	}

	resultPath := filepath.Join(s.Pending, rid+".result")
	data, err := os.ReadFile(resultPath)
	if err != nil {
		// Verdict recorded, but result file not yet written
		if v.Decision == "cancelled" {
			return protocol.StatusResponse{
				ID:        rid,
				Status:    "cancelled",
				Decision:  "cancelled",
				By:        v.By,
				Argv:      rec.Argv,
				Cwd:       rec.Cwd,
				UID:       rec.UID,
				AsUID:     rec.AsUID,
				Exit:      -1,
				Step:      step,
				ConfirmOf: rec.ConfirmOf,
				Preview:   rec.Preview,
				AuthURL:   rec.AuthURL,
			}
		}
		if v.Decision == "approve" {
			// If this was an initial request, check if a child confirmation is awaiting verdict
			if step == "initial" {
				if childRID, childRec := s.FindConfirmation(rid); childRec != nil {
					if c := s.CancelInfo(childRID); c != nil {
						return protocol.StatusResponse{
							ID:        rid,
							Status:    "cancelled",
							Decision:  "cancelled",
							By:        c.By,
							Output:    c.Reason,
							Argv:      rec.Argv,
							Cwd:       rec.Cwd,
							UID:       rec.UID,
							AsUID:     rec.AsUID,
							Exit:      -1,
							Step:      step,
							ConfirmOf: rec.ConfirmOf,
							Preview:   rec.Preview,
							AuthURL:   rec.AuthURL,
						}
					}
					if s.Verdict(childRID) == nil {
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
							AsUID:     rec.AsUID,
							ExpiresIn: expiresIn,
							Exit:      -1,
							Step:      step,
							ConfirmOf: rec.ConfirmOf,
							Preview:   rec.Preview,
							AuthURL:   rec.AuthURL,
						}
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
				AsUID:     rec.AsUID,
				Exit:      -1,
				Step:      step,
				ConfirmOf: rec.ConfirmOf,
				Preview:   rec.Preview,
				AuthURL:   rec.AuthURL,
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
			AsUID:     rec.AsUID,
			Exit:      -1,
			Step:      step,
			ConfirmOf: rec.ConfirmOf,
			Preview:   rec.Preview,
			AuthURL:   rec.AuthURL,
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
	} else if v.Decision == "cancelled" || r.Output == "cancelled" {
		status = "cancelled"
		decision = "cancelled"
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
			if c := s.CancelInfo(childRID); c != nil {
				decision = "cancelled"
				status = "cancelled"
				if c.Reason != "" {
					r.Output = c.Reason
				}
			} else if cv := s.Verdict(childRID); cv != nil {
				decision = cv.Decision
				if cv.Decision == "deny" {
					status = "denied"
				} else if cv.Decision == "timeout" || cv.Decision == "confirmation_timeout" {
					status = "timeout"
				} else if cv.Decision == "client_aborted" {
					status = "client_aborted"
				} else if cv.Decision == "cancelled" {
					status = "cancelled"
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
		AsUID:     rec.AsUID,
		Exit:      r.Exit,
		Output:    r.Output,
		Step:      step,
		ConfirmOf: rec.ConfirmOf,
		Preview:   rec.Preview,
		AuthURL:   rec.AuthURL,
	}
}

func (s Store) Append(ev protocol.AuditEvent) {
	ev.TS = time.Now().UTC().Format(time.RFC3339)
	data, err := json.Marshal(ev)
	if err != nil {
		return
	}
	_ = appendNoFollow(s.Audit, append(data, '\n'), 0o600)
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
	_ = writeNoFollow(s.Offset, []byte(itoa(n)), 0o600, true)
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
