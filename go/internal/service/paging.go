// Paged Telegram list cards: a long list is split into bounded pages and
// navigated with inline Prev/Next buttons. The callback seam mirrors ask —
// data travels as "page:<rid>:<n>", Poll forwards it, and TelegramPump
// re-renders the stored card in place. Paging is non-binding and
// idempotent (re-tapping a page yields the same card), so it never
// consumes a verdict or selection.
package service

import (
	"fmt"

	"2fado/internal/telegram"
)

// SendList posts the first page of a list card and persists the backing
// lines on rid so page callbacks can re-render it. rid must name an
// existing stored record (callers save it first, exactly as ask/notify
// do). Falls back to stdout when Telegram is disabled.
func (s Service) SendList(rid, title string, lines []string) (int64, error) {
	if _, err := s.Store.Load(rid); err != nil {
		return 0, fmt.Errorf("paged list %q: %w", rid, err)
	}
	s.Store.SetPageList(rid, title, lines)
	pages := telegram.ListPages(len(lines))
	if !s.notifyTelegram() || s.isPlaceholder() {
		fmt.Printf("[pager:stdout] list %s: %s\n", rid, title)
		for _, l := range lines {
			fmt.Printf("  %s\n", l)
		}
		return 0, nil
	}
	mid, err := s.activeTG().SendWithMarkup(s.activeChatID(),
		telegram.PagedCard(s.Host, title, rid, telegram.PageSlice(lines, 0), 0, pages),
		0, telegram.PagedButtons(rid, 0, pages))
	if err != nil {
		return 0, err
	}
	s.Store.AttachPager(rid, s.activeChatID(), mid)
	return mid, nil
}

// repage re-renders a stored list card at the requested 0-based page. It
// is the TelegramPump handler for page callbacks and deliberately writes
// nothing through the verdict or selection paths.
func (s Service) repage(rid string, page int) {
	if !s.notifyTelegram() || s.isPlaceholder() {
		return
	}
	rec, err := s.Store.Load(rid)
	if err != nil || rec.ChatID == "" || rec.MsgID == 0 {
		return
	}
	pages := telegram.ListPages(len(rec.PageLines))
	if page < 0 || page >= pages {
		return
	}
	title := rec.Summary
	if title == "" {
		title = "List"
	}
	s.activeTG().Edit(rec.ChatID, rec.MsgID,
		telegram.PagedCard(s.Host, title, rid, telegram.PageSlice(rec.PageLines, page), page, pages),
		telegram.PagedButtons(rid, page, pages))
}
