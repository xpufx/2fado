// Notify-only petitions: no-exec human-presence records (2FA links,
// staged approvals, FYI+ack). The agent posts link+summary; the operator
// acks from Paseo/Telegram/CLI; the agent observes ack via status.
package service

import (
	"fmt"
	"strings"
	"time"

	"2fado/internal/protocol"
	"2fado/internal/telegram"
)

// Notify TTL bounds: long-but-finite. Ack clears early; expiry prunes.
const (
	DefaultNotifyTTL = 24 * time.Hour
	MinNotifyTTL     = 5 * time.Minute
	MaxNotifyTTL     = 7 * 24 * time.Hour
	maxNotifyLink    = 2000
	maxNotifySummary = 2000
)

// Notify creates a no-exec notify petition and pages the operator.
// Returns Status pending + ID on success, denied + Reason on bad input.
func (s Service) Notify(req protocol.NotifyRequest, uid uint32) protocol.RunResult {
	link := strings.TrimSpace(req.Link)
	summary := strings.TrimSpace(req.Summary)
	if link == "" || summary == "" {
		return protocol.RunResult{Status: "denied", Reason: "link and summary required"}
	}
	if len(link) > maxNotifyLink || len(summary) > maxNotifySummary {
		return protocol.RunResult{Status: "denied", Reason: "link or summary too long"}
	}
	if !strings.HasPrefix(link, "http://") && !strings.HasPrefix(link, "https://") {
		return protocol.RunResult{Status: "denied", Reason: "link must be http(s) URL"}
	}
	ttl := time.Duration(req.TTLSeconds) * time.Second
	if ttl == 0 {
		ttl = DefaultNotifyTTL
	}
	if ttl < MinNotifyTTL {
		ttl = MinNotifyTTL
	}
	if ttl > MaxNotifyTTL {
		ttl = MaxNotifyTTL
	}
	cwd := ""
	rid := newRID()
	expires := time.Now().Add(ttl)
	_ = s.Store.Save(protocol.PendingRecord{
		UID:     uid,
		Cwd:     cwd,
		Expires: expires.Unix(),
		Step:    "initial",
		Kind:    "notify",
		Link:    link,
		Summary: summary,
	}, rid)
	s.Store.Append(protocol.AuditEvent{Ev: "notify", UID: uid,
		RID: rid, Step: "initial", Link: link, Summary: summary})
	s.pageNotify(rid, link, summary, uid, ttl)
	return protocol.RunResult{Status: "pending", ID: rid}
}

// Ack records a non-binding operator acknowledgement on a notify petition.
// First ack wins; exec petitions and unknown IDs are rejected.
func (s Service) Ack(sub protocol.AckSubmit, callerUID uint32) protocol.AckResponse {
	if !isAdminUID(callerUID) {
		s.Store.Append(protocol.AuditEvent{Ev: "ack_unauthorized", By: cleanBy(sub.By), RID: sub.ID})
		return protocol.AckResponse{Error: "unauthorized"}
	}
	by := cleanBy(sub.By)
	if !s.Store.Ack(sub.ID, by) {
		return protocol.AckResponse{}
	}
	s.Store.Append(protocol.AuditEvent{Ev: "ack", RID: sub.ID, By: by})
	s.closeNotifyCard(sub.ID, by)
	return protocol.AckResponse{Acked: true}
}

func (s Service) pageNotify(rid, link, summary string, uid uint32, ttl time.Duration) {
	left := int64(ttl / time.Second)
	if s.notifyTelegram() && !s.isPlaceholder() {
		cid := s.activeChatID()
		tg := s.activeTG()
		text := telegram.NotifyCard(s.Host, link, summary, rid, left, false, "")
		if mid, err := tg.SendWithMarkup(cid, text, 0, telegram.NotifyButtons(link, rid)); err == nil {
			s.Store.AttachPager(rid, cid, mid)
			return
		}
		fmt.Printf("[pager:stdout] notify %s: %s\n%s\n", rid, summary, link)
		return
	}
	fmt.Printf("[pager:stdout] notify %s: %s\n%s\n(ack: 2fado ack %s)\n", rid, summary, link, rid)
}

func (s Service) closeNotifyCard(rid, by string) {
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
		telegram.NotifyCard(s.Host, rec.Link, rec.Summary, rid, left, true, by),
		telegram.EmptyButtons())
}
