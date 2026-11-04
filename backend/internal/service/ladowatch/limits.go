package ladowatch

// Usage limit messages (plan step 12, decision 5). What the agents write when
// a subscription's usage window runs out, as read from the installed
// binaries and from AO's own records of real sessions:
//
//   - Claude Code (2.1.317): "You've hit your <limit> · resets <time> (<zone>)",
//     where <limit> is "session limit", "5-hour limit", "weekly limit", "Opus
//     limit", "Sonnet limit" or "Fable limit", and <time> is "8:20pm" or
//     "7pm" when the reset is within a day, and "Oct 31, 11am" (with the year
//     after the day when it is another year's) when it is later. A
//     " · progress saved" may follow. A compaction that hit the limit reads
//     "Prompt is too long · automatic compaction failed: You've hit your …".
//   - Codex (codex-cli 0.166): "You've hit your usage limit. … try again at
//     8:20 PM." the same day and "… try again at Oct 31st, 2026 11:00 AM."
//     later, in the computer's own time zone, or "… try again later." with
//     no time.
//
// In Chat, the only way LADO runs workers, AO keeps the message as the error
// of the turn that failed (conversation_turns.error_message); on 10-29 every
// limit stop of a real worker was there, and an assistant message only ever
// repeated it. Spend limits ("monthly spend limit", "team's shared budget")
// do not lift with a usage window, so LADO does not take them as one.

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// limitHit finds a usage limit message and names the limit.
var limitHit = regexp.MustCompile(`You['’]ve hit your ([A-Za-z0-9 -]+?limit)\b`)

// windowLimits are the limits that lift when a usage window resets.
var windowLimits = map[string]bool{
	"session limit": true,
	"5-hour limit":  true,
	"weekly limit":  true,
	"opus limit":    true,
	"sonnet limit":  true,
	"fable limit":   true,
	"usage limit":   true,
}

// claudeReset reads Claude Code's reset time: "resets 8:20pm (Asia/Seoul)" or
// "resets Oct 31, 11am (Asia/Seoul)", with an optional year after the day.
var claudeReset = regexp.MustCompile(`resets (?:([A-Z][a-z]{2}) (\d{1,2}),(?: (\d{4}),)? )?(\d{1,2})(?::(\d{2}))?\s?([ap]m)(?: \(([^)]+)\))?`)

// codexReset reads Codex's reset time: "try again at 8:20 PM" or "try again
// at Oct 31st, 2026 11:00 AM".
var codexReset = regexp.MustCompile(`(?i)try again at (?:([A-Z][a-z]{2}) (\d{1,2})(?:st|nd|rd|th)?, (\d{4}) )?(\d{1,2}):(\d{2}) ?([AP]M)`)

// limitMessage is a usage limit message read from a worker's turn.
type limitMessage struct {
	// Name is the limit, as "session limit".
	Name string
	// ResetAt is when the limit lifts; Guessed marks a time LADO could not
	// read and took as LadoLimitGuessedReset after the message.
	ResetAt time.Time
	Guessed bool
}

// parseLimit reads a usage limit message out of text, which a worker wrote at
// seen. local is the time zone a reset without one is read in: the
// computer's, where the agent runs. ok is false when text holds no message of
// a limit that lifts with a usage window.
func parseLimit(text string, seen time.Time, local *time.Location, guess time.Duration) (limitMessage, bool) {
	m := limitHit.FindStringSubmatchIndex(text)
	if m == nil {
		return limitMessage{}, false
	}
	name := strings.TrimSpace(text[m[2]:m[3]])
	if !windowLimits[strings.ToLower(name)] {
		return limitMessage{}, false
	}
	rest := text[m[1]:]
	if at, ok := readClaudeReset(rest, seen, local); ok {
		return limitMessage{Name: name, ResetAt: at}, true
	}
	if at, ok := readCodexReset(rest, seen, local); ok {
		return limitMessage{Name: name, ResetAt: at}, true
	}
	return limitMessage{Name: name, ResetAt: seen.Add(guess), Guessed: true}, true
}

func readClaudeReset(text string, seen time.Time, local *time.Location) (time.Time, bool) {
	g := claudeReset.FindStringSubmatch(text)
	if g == nil {
		return time.Time{}, false
	}
	loc := local
	if g[7] != "" {
		if zone, err := time.LoadLocation(strings.TrimSpace(g[7])); err == nil {
			loc = zone
		}
	}
	hour, ok := clock12(g[4], g[6])
	if !ok {
		return time.Time{}, false
	}
	minute := 0
	if g[5] != "" {
		minute, _ = strconv.Atoi(g[5])
	}
	return resolveReset(seen, loc, g[1], g[2], g[3], hour, minute)
}

func readCodexReset(text string, seen time.Time, local *time.Location) (time.Time, bool) {
	g := codexReset.FindStringSubmatch(text)
	if g == nil {
		return time.Time{}, false
	}
	hour, ok := clock12(g[4], g[6])
	if !ok {
		return time.Time{}, false
	}
	minute, _ := strconv.Atoi(g[5])
	return resolveReset(seen, local, g[1], g[2], g[3], hour, minute)
}

// clock12 turns a 12-hour clock's hour and its am or pm into a 24-hour hour.
func clock12(hour, half string) (int, bool) {
	h, err := strconv.Atoi(hour)
	if err != nil || h < 1 || h > 12 {
		return 0, false
	}
	h %= 12
	if strings.EqualFold(half, "pm") {
		h += 12
	}
	return h, true
}

// resolveReset places a reset's clock time, and its month, day and year when
// the message gave them, in loc after seen: a time alone is its next
// occurrence, a day without a year its next one.
func resolveReset(seen time.Time, loc *time.Location, month, day, year string, hour, minute int) (time.Time, bool) {
	if loc == nil {
		loc = time.Local
	}
	if minute < 0 || minute > 59 {
		return time.Time{}, false
	}
	at := seen.In(loc)
	if month == "" {
		reset := time.Date(at.Year(), at.Month(), at.Day(), hour, minute, 0, 0, loc)
		// The message rounds the reset down to its minute.
		if reset.Before(at.Add(-time.Minute)) {
			reset = reset.AddDate(0, 0, 1)
		}
		return reset.UTC(), true
	}
	mon, err := time.Parse("Jan", month)
	if err != nil {
		return time.Time{}, false
	}
	d, err := strconv.Atoi(day)
	if err != nil || d < 1 || d > 31 {
		return time.Time{}, false
	}
	y := at.Year()
	if year != "" {
		if y, err = strconv.Atoi(year); err != nil {
			return time.Time{}, false
		}
	}
	reset := time.Date(y, mon.Month(), d, hour, minute, 0, 0, loc)
	if year == "" && reset.Before(at.AddDate(0, 0, -1)) {
		reset = reset.AddDate(1, 0, 0)
	}
	return reset.UTC(), true
}
