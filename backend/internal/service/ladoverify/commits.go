package ladoverify

// The commit_messages item (decision f of the step 8 plan; docs/LJY-PRD/
// HARNESS.md §5): the commits after the merge-base, and the pull request's
// title, which a squash merge makes its commit's subject, are in Korean. A
// commit-msg hook never sees a commit made with --no-verify, so LADO judges
// the commits of the head (PRD gate 9). The judgement is a heuristic: a
// subject with Hangul is Korean, a sentence of English words is not, and a
// subject that is short or only code goes to the developer to confirm.

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// The validator's results for a commit or the PR title.
const (
	MessageOK        = "ok"
	MessageNotKorean = "not_korean"
	MessageUnclear   = "unclear"
	// MessageBodyUnclear is a commit whose subject is Korean and whose body
	// reads as English.
	MessageBodyUnclear = "body_unclear"
)

const (
	// subjectWordsMin and subjectLongWordsMin are how many Latin words, and
	// of them words of three letters or more, a subject without Hangul has
	// when it reads as English. Shorter subjects, as "bump deps", may be
	// shorthand and are unclear.
	subjectWordsMin     = 3
	subjectLongWordsMin = 2
	// bodyWordsMin is how many Latin words make a body without Hangul
	// unclear.
	bodyWordsMin = 5
)

var (
	// conventionalPrefix matches a Conventional Commit prefix, as
	// "feat(api)!: ", with its type in the first group.
	conventionalPrefix = regexp.MustCompile(`^([^\s()!:]+)(\([^)]*\))?!?:\s*`)
	// backtickSpan matches inline code, or a fenced block in a body.
	backtickSpan = regexp.MustCompile("`[^`]*`")
	// trailerLine matches a trailer, as "Co-Authored-By: ..." or
	// "Signed-off-by: ...".
	trailerLine = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]*: `)
)

// JudgeSubject judges a commit's subject, or a pull request's title. It cuts
// one Conventional Commit prefix whose type cfg allows, then the code in the
// rest (see scanProse): a Hangul syllable left makes it MessageOK; three
// Latin words or more, two of them with three letters or more, make it
// MessageNotKorean; anything else is MessageUnclear.
func JudgeSubject(cfg domain.LadoCommitLocaleConfig, subject string) string {
	p := scanProse(stripPrefix(cfg.AllowPrefixes, strings.TrimSpace(subject)))
	switch {
	case p.hangul:
		return MessageOK
	case p.words >= subjectWordsMin && p.longWords >= subjectLongWordsMin:
		return MessageNotKorean
	default:
		return MessageUnclear
	}
}

// JudgeBody judges the body of a commit message, the lines after its
// subject. Trailers are left out; a body without a Hangul syllable and with
// five Latin words or more is MessageUnclear, any other MessageOK.
func JudgeBody(body string) string {
	var lines []string
	for line := range strings.SplitSeq(body, "\n") {
		if line = strings.TrimSpace(line); !trailerLine.MatchString(line) {
			lines = append(lines, line)
		}
	}
	if p := scanProse(strings.Join(lines, "\n")); !p.hangul && p.words >= bodyWordsMin {
		return MessageUnclear
	}
	return MessageOK
}

// EvaluateCommits computes the commit_messages item from the config of the
// snapshot's commit_message_locale validator, nil when the snapshot has none
// (the rule is off, or the snapshot is older than lado-policy/3), the
// commits after the merge-base, oldest first, and the PR's title. A subject
// or the title that is not Korean fails the item; an unclear one, or a
// Korean subject over an English body, needs the developer's confirmation.
// Without a commit nothing lands on the base branch, and the item passes.
func EvaluateCommits(cfg *domain.LadoCommitLocaleConfig, commits []Commit, prTitle string) (domain.LadoVerifyItem, error) {
	if cfg == nil {
		return domain.NewLadoVerifyItem(domain.LadoVerifyCommitMessages, domain.LadoVerifyInfo, domain.LadoCodeNotRequired, nil)
	}
	if cfg.Language != "" && cfg.Language != "ko" {
		return domain.LadoVerifyItem{}, fmt.Errorf("evaluate the commit messages: LADO judges Korean only, not %q", cfg.Language)
	}
	details := domain.LadoCommitMessagesDetails{PRTitle: domain.LadoVerifyText{Text: prTitle, Result: JudgeSubject(*cfg, prTitle)}}
	results := []string{details.PRTitle.Result}
	for _, commit := range commits {
		subject, body, _ := strings.Cut(commit.Message, "\n")
		subject = strings.TrimSpace(subject)
		result := JudgeSubject(*cfg, subject)
		if result == MessageOK && JudgeBody(body) == MessageUnclear {
			result = MessageBodyUnclear
		}
		details.Commits = append(details.Commits, domain.LadoVerifyCommit{SHA: commit.SHA, Subject: subject, Result: result})
		results = append(results, result)
	}
	outcome, code := domain.LadoVerifyPass, ""
	switch {
	case len(commits) == 0:
	case slices.Contains(results, MessageNotKorean):
		outcome, code = domain.LadoVerifyFail, domain.LadoCodeCommitMessagesFailed
	case slices.Contains(results, MessageUnclear) || slices.Contains(results, MessageBodyUnclear):
		outcome, code = domain.LadoVerifyConfirm, domain.LadoCodeCommitMessagesUnclear
	}
	return domain.NewLadoVerifyItem(domain.LadoVerifyCommitMessages, outcome, code, details)
}

// stripPrefix cuts a Conventional Commit prefix whose type is one of types.
// Types compare without case, as the Conventional Commits specification
// asks.
func stripPrefix(types []string, subject string) string {
	m := conventionalPrefix.FindStringSubmatchIndex(subject)
	if m == nil {
		return subject
	}
	typ := subject[m[2]:m[3]]
	if !slices.ContainsFunc(types, func(t string) bool { return strings.EqualFold(t, typ) }) {
		return subject
	}
	return subject[m[1]:]
}

// prose is what a text says once the code is out of it.
type prose struct {
	hangul bool
	// words counts the runs of Latin letters, and longWords those of three
	// letters or more.
	words, longWords int
}

// scanProse takes the code out of text: inline code in backticks, and each
// word that is a URL, a path or a file name (it holds a slash, a backslash
// or a dot), or an identifier, an issue reference or a number (it holds a
// digit, an underscore or a capital after a letter). Brackets, quotes and
// sentence marks around a word do not count. A word with a Hangul syllable
// is Korean; the Latin letters of the other words count as words.
func scanProse(text string) prose {
	var p prose
	for _, word := range strings.Fields(backtickSpan.ReplaceAllString(text, " ")) {
		word = trimPunct(word)
		switch {
		case word == "", strings.ContainsAny(word, `/\.`):
			// A URL, a path or a file name.
		case strings.ContainsFunc(word, isHangulSyllable):
			p.hangul = true
		case isCode(word):
			// An identifier, an issue reference or a number.
		default:
			for _, letters := range latinRuns(word) {
				p.words++
				if letters >= 3 {
					p.longWords++
				}
			}
		}
	}
	return p
}

// trimPunct cuts the opening brackets and quotes before a word and the
// closing ones and sentence marks after it, so a sentence's last dot goes
// and a file name's dot stays.
func trimPunct(word string) string {
	word = strings.TrimLeftFunc(word, func(r rune) bool {
		return unicode.In(r, unicode.Ps, unicode.Pi) || r == '"' || r == '\''
	})
	return strings.TrimRightFunc(word, func(r rune) bool {
		return unicode.In(r, unicode.Pe, unicode.Pf) || strings.ContainsRune(`.,;:!?"'…`, r)
	})
}

// isCode reports whether a word is an identifier, an issue reference or a
// number: it holds a digit, an underscore, or a capital right after a
// letter, as fooBar or JSON.
func isCode(word string) bool {
	afterLetter := false
	for _, r := range word {
		if r == '_' || unicode.IsDigit(r) || (afterLetter && unicode.IsUpper(r)) {
			return true
		}
		afterLetter = unicode.IsLetter(r)
	}
	return false
}

// latinRuns returns the length, in letters, of each run of Latin letters in
// word.
func latinRuns(word string) []int {
	var runs []int
	n := 0
	for _, r := range word + " " {
		if unicode.Is(unicode.Latin, r) {
			n++
			continue
		}
		if n > 0 {
			runs = append(runs, n)
		}
		n = 0
	}
	return runs
}

// isHangulSyllable reports whether r is a precomposed Hangul syllable,
// U+AC00 to U+D7A3.
func isHangulSyllable(r rune) bool { return r >= 0xAC00 && r <= 0xD7A3 }
