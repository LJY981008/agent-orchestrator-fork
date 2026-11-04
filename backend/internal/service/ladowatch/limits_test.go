package ladowatch

import (
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestParseLimitReadsTheRealMessages(t *testing.T) {
	loc := seoul(t)
	kst := func(y int, m time.Month, d, h, min int) time.Time { return time.Date(y, m, d, h, min, 0, 0, loc).UTC() }
	// The worker stopped at 16:33 in Seoul on 10-29.
	seen := kst(2026, 10, 29, 16, 33)
	guess := domain.LadoLimitGuessedReset
	cases := []struct {
		name    string
		text    string
		ok      bool
		limit   string
		reset   time.Time
		guessed bool
	}{
		// Claude Code, as AO stored them on 10-28 and 10-29.
		{"session limit the same evening", "You've hit your session limit · resets 8:20pm (Asia/Seoul)", true, "session limit", kst(2026, 10, 29, 20, 20), false},
		{"an hour without minutes", "You've hit your session limit · resets 7pm (Asia/Seoul)", true, "session limit", kst(2026, 10, 29, 19, 0), false},
		{"the next morning", "You've hit your session limit · resets 5:30am (Asia/Seoul)", true, "session limit", kst(2026, 10, 30, 5, 30), false},
		{"weekly limit with a day", "You've hit your weekly limit · resets Oct 31, 11am (Asia/Seoul)", true, "weekly limit", kst(2026, 10, 31, 11, 0), false},
		{"a failed compaction", "Prompt is too long · automatic compaction failed: You've hit your session limit · resets 8:20pm (Asia/Seoul)", true, "session limit", kst(2026, 10, 29, 20, 20), false},
		// Claude Code 2.1.317's other names and suffix.
		{"5-hour limit with progress saved", "You've hit your 5-hour limit · resets 9:05pm (Asia/Seoul) · progress saved", true, "5-hour limit", kst(2026, 10, 29, 21, 5), false},
		{"another year", "You've hit your weekly limit · resets Jan 2, 2027, 3:30pm (Asia/Seoul)", true, "weekly limit", kst(2027, 1, 2, 15, 30), false},
		{"another zone", "You've hit your session limit · resets 8pm (UTC)", true, "session limit", time.Date(2026, 10, 29, 20, 0, 0, 0, time.UTC), false},
		{"a day early next year", "You've hit your weekly limit · resets Jan 3, 9am (Asia/Seoul)", true, "weekly limit", kst(2027, 1, 3, 9, 0), false},
		// Codex 0.166, in the computer's own zone.
		{"codex the same day", "You've hit your usage limit. Upgrade to Pro (https://chatgpt.com/explore/pro), visit https://chatgpt.com/settings/usage to purchase more credits or try again at 8:20 PM.", true, "usage limit", kst(2026, 10, 29, 20, 20), false},
		{"codex another day", "You've hit your usage limit. Try again at Oct 31st, 2026 11:00 AM.", true, "usage limit", kst(2026, 10, 31, 11, 0), false},
		{"codex without a time", "You've hit your usage limit. Visit https://chatgpt.com/settings/usage to purchase more credits or try again later.", true, "usage limit", seen.Add(guess), true},
		{"a curly apostrophe", "You’ve hit your session limit · resets 8:20pm (Asia/Seoul)", true, "session limit", kst(2026, 10, 29, 20, 20), false},
		{"a limit without a time", "You've hit your session limit", true, "session limit", seen.Add(guess), true},
		// Not a usage window.
		{"monthly spend limit", "You've hit your monthly spend limit. Run /usage-credits to raise it", false, "", time.Time{}, false},
		{"team budget", "You've hit your team's shared budget. Switch to another model to continue.", false, "", time.Time{}, false},
		{"another error", "Prompt is too long", false, "", time.Time{}, false},
		{"empty", "", false, "", time.Time{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseLimit(tc.text, seen, loc, guess)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v (%+v)", ok, tc.ok, got)
			}
			if !ok {
				return
			}
			if got.Name != tc.limit || !got.ResetAt.Equal(tc.reset) || got.Guessed != tc.guessed {
				t.Fatalf("got %s at %s guessed %v, want %s at %s guessed %v", got.Name, got.ResetAt.In(loc), got.Guessed, tc.limit, tc.reset.In(loc), tc.guessed)
			}
		})
	}
}

func TestParseLimitTakesAResetJustPassedAsThisMinute(t *testing.T) {
	loc := seoul(t)
	// The message rounds the reset down; seen 30 seconds into the minute is
	// still the same reset, not tomorrow's.
	seen := time.Date(2026, 10, 29, 20, 20, 30, 0, loc)
	got, ok := parseLimit("You've hit your session limit · resets 8:20pm (Asia/Seoul)", seen, loc, time.Hour)
	if !ok || !got.ResetAt.Equal(time.Date(2026, 10, 29, 20, 20, 0, 0, loc)) {
		t.Fatalf("got %v %v", got.ResetAt.In(loc), ok)
	}
}
