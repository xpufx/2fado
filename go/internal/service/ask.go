// Ask petitions: interactive multi-choice questions. The agent posts a
// question with N options; the operator picks one over Telegram (or a
// future transport); the agent observes the selection on the same socket
// call. The MVP is single-select only; MultiSelect, AllowWriteIn, and
// RecommendedIndex are persisted from day one so the full ask_question
// matrix can land later without a data migration.
package service

import (
	"fmt"
	"strings"
	"time"

	"2fado/internal/protocol"
	"2fado/internal/telegram"
)

// Ask TTL bounds: short enough to unblock an agent quickly, long enough
// for a human to notice the page.
const (
	DefaultAskTTL  = time.Hour
	MinAskTTL      = 10 * time.Second
	MaxAskTTL      = 7 * 24 * time.Hour
	maxAskQuestion = 2000
	maxAskOption   = 500
	maxAskOptions  = 10
)

// Ask creates an interactive ask petition, pages the operator, and blocks
// until a selection lands or the TTL expires. Returns selected + the
// chosen option, timeout, or denied + Reason on bad input.
func (s Service) Ask(req protocol.AskRequest, uid uint32) protocol.AskResult {
	return s.AskWithCancel(req, uid, nil)
}

// AskWithCancel is Ask with early cancellation (caller disconnected).
func (s Service) AskWithCancel(req protocol.AskRequest, uid uint32, cancel <-chan struct{}) protocol.AskResult {
	question := strings.TrimSpace(req.Question)
	link := strings.TrimSpace(req.Link)
	opts := make([]string, 0, len(req.Options))
	for _, o := range req.Options {
		if o = strings.TrimSpace(o); o != "" {
			opts = append(opts, o)
		}
	}
	if question == "" || len(opts) < 2 {
		return protocol.AskResult{Status: "denied", Reason: "question and at least two options required"}
	}
	if len(opts) > maxAskOptions {
		return protocol.AskResult{Status: "denied", Reason: "too many options"}
	}
	if len(question) > maxAskQuestion {
		return protocol.AskResult{Status: "denied", Reason: "question too long"}
	}
	for _, o := range opts {
		if len(o) > maxAskOption {
			return protocol.AskResult{Status: "denied", Reason: "option too long"}
		}
	}
	if link != "" && !strings.HasPrefix(link, "http://") && !strings.HasPrefix(link, "https://") {
		return protocol.AskResult{Status: "denied", Reason: "link must be http(s) URL"}
	}
	recommended := -1 // 0-based render index; wire field is 1-based, 0 = none
	if req.RecommendedIndex >= 1 && req.RecommendedIndex <= len(opts) {
		recommended = req.RecommendedIndex - 1
	}
	ttl := time.Duration(req.TTLSeconds) * time.Second
	if ttl == 0 {
		ttl = DefaultAskTTL
	}
	if ttl < MinAskTTL {
		ttl = MinAskTTL
	}
	if ttl > MaxAskTTL {
		ttl = MaxAskTTL
	}
	rid := newRID()
	expires := time.Now().Add(ttl)
	_ = s.Store.Save(protocol.PendingRecord{
		UID:              uid,
		Expires:          expires.Unix(),
		Step:             "initial",
		Kind:             "ask",
		Question:         question,
		Options:          opts,
		Link:             link,
		MultiSelect:      req.MultiSelect,
		AllowWriteIn:     req.AllowWriteIn,
		RecommendedIndex: req.RecommendedIndex,
	}, rid)
	s.Store.Append(protocol.AuditEvent{Ev: "ask", UID: uid, RID: rid, Step: "initial",
		Question: question, Options: opts, Link: link})
	s.pageAsk(rid, question, link, opts, recommended, ttl)

	sel, aborted := s.awaitSelection(rid, expires, cancel)
	if aborted {
		s.closeAskCard(rid, "", "client")
		s.Store.Append(protocol.AuditEvent{Ev: "client_aborted", UID: uid, RID: rid})
		return protocol.AskResult{Status: "denied", ID: rid, Question: question, Reason: "client_aborted"}
	}
	if sel == nil {
		s.closeAskCard(rid, "", "")
		s.Store.Append(protocol.AuditEvent{Ev: "ask_timeout", UID: uid, RID: rid})
		return protocol.AskResult{Status: "timeout", ID: rid, Question: question, Reason: "timeout"}
	}
	s.Store.Append(protocol.AuditEvent{Ev: "ask_select", RID: rid, By: sel.By,
		Selection: sel.Selection, SelectionIdx: sel.SelectionIdx})
	s.closeAskCard(rid, sel.Selection, sel.By)
	return protocol.AskResult{Status: "selected", ID: rid, Question: question,
		Selection: sel.Selection, SelectionIdx: sel.SelectionIdx, By: sel.By}
}

// awaitSelection polls the store for a recorded choice. It returns
// (nil, false) on expiry and (nil, true) when the caller disconnected.
func (s Service) awaitSelection(rid string, expires time.Time, cancel <-chan struct{}) (*protocol.SelectionRecord, bool) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		if sel := s.Store.SelectionInfo(rid); sel != nil {
			return sel, false
		}
		if time.Now().After(expires) {
			return nil, false
		}
		select {
		case <-cancel:
			return nil, true
		case <-ticker.C:
		}
	}
}

func (s Service) pageAsk(rid, question, link string, opts []string, recommended int, ttl time.Duration) {
	left := int64(ttl / time.Second)
	if s.notifyTelegram() && !s.isPlaceholder() {
		cid := s.activeChatID()
		tg := s.activeTG()
		text := telegram.AskCard(s.Host, question, link, rid, left, false, "", "")
		if mid, err := tg.SendWithMarkup(cid, text, 0, telegram.AskButtons(rid, opts, recommended)); err == nil {
			s.Store.AttachPager(rid, cid, mid)
			return
		}
	}
	fmt.Printf("[pager:stdout] ask %s: %s\n", rid, question)
	if link != "" {
		fmt.Printf("  link: %s\n", link)
	}
	for i, o := range opts {
		fmt.Printf("  [%d] %s\n", i, o)
	}
}

// closeAskCard rewrites the posted ask card: with a selection it shows
// "Chosen: <option>", otherwise it marks the petition closed. Either way
// the option buttons are stripped.
func (s Service) closeAskCard(rid, selection, by string) {
	if !s.notifyTelegram() || s.isPlaceholder() {
		return
	}
	rec, err := s.Store.Load(rid)
	if err != nil || rec.ChatID == "" {
		return
	}
	left := rec.Expires - time.Now().Unix()
	if left < 0 {
		left = 0
	}
	s.activeTG().Edit(rec.ChatID, rec.MsgID,
		telegram.AskCard(s.Host, rec.Question, rec.Link, rid, left, selection != "", selection, by),
		telegram.EmptyButtons())
}
