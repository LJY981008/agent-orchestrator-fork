package ladoverify

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// checkCommits evaluates the commit messages with cfg and compares the item
// with the outcome, code and details wanted.
func checkCommits(t *testing.T, cfg *domain.LadoCommitLocaleConfig, commits []Commit, prTitle string, outcome domain.LadoVerifyOutcome, code string, details any) {
	t.Helper()
	got, err := EvaluateCommits(cfg, commits, prTitle)
	if err != nil {
		t.Fatalf("EvaluateCommits = %v", err)
	}
	want, err := domain.NewLadoVerifyItem(domain.LadoVerifyCommitMessages, outcome, code, details)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("commit_messages = %s %q %s\nwant %s %q %s", got.Outcome, got.Code, got.Details, want.Outcome, want.Code, want.Details)
	}
}

// HARNESS §15 item 5: a clearly English commit or PR title blocks the
// integration, Korean with the configured prefixes passes, and what is short
// or only code goes to the developer.
func TestKoreanCommitValidator(t *testing.T) {
	cfg := domain.DefaultLadoCommitLocaleConfig()

	t.Run("subjects", func(t *testing.T) {
		for _, tc := range []struct{ subject, want string }{
			{"feat: 로그인 추가", MessageOK},
			{"fix(api): 토큰 만료 처리", MessageOK},
			{"feat(ui)!: 설정 화면 개편", MessageOK},
			{"로그인 추가", MessageOK},
			{"Add login page", MessageNotKorean},
			{"Fix the login timeout.", MessageNotKorean},
			{"fix: typo", MessageUnclear},
			// Two words may be shorthand, so a short subject is unclear.
			{"chore: bump deps", MessageUnclear},
			{"refactor: `foo_bar` 정리", MessageOK},
			// Only an allowed type's prefix is cut, compared without case,
			// and only one.
			{"feat: add login", MessageUnclear},
			{"wip: add login", MessageNotKorean},
			{"Fix(API): add login", MessageUnclear},
			{"fix: fix: add login", MessageNotKorean},
			// Issue references, URLs, paths, file names, identifiers and
			// numbers are code, not prose.
			{"fix: #12 ABC-34 (https://example.com/a) src/app.go .gitignore fooBar foo_bar JSON v2 2026", MessageUnclear},
			{"Update the README.md file.", MessageNotKorean},
			{"docs: README.md를 고침", MessageOK},
			{"fix: login을 고침", MessageOK},
			{"", MessageUnclear},
			{"修复登录问题", MessageUnclear},
		} {
			if got := JudgeSubject(cfg, tc.subject); got != tc.want {
				t.Errorf("JudgeSubject(%q) = %s, want %s", tc.subject, got, tc.want)
			}
		}
	})

	t.Run("bodies", func(t *testing.T) {
		for _, tc := range []struct{ body, want string }{
			{"", MessageOK},
			{"\nThe login page now refreshes the token.", MessageUnclear},
			{"\nCo-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>\nSigned-off-by: LADO <lado@example.com>", MessageOK},
			{"\n토큰이 만료되기 전에 다시 받는다.\nThe login page refreshes the token.\n", MessageOK},
			{"\nFour words are fine.", MessageOK},
			{"\nSee `make test lint vet race` and docs/the-guide.md for 2 cases.", MessageOK},
		} {
			if got := JudgeBody(tc.body); got != tc.want {
				t.Errorf("JudgeBody(%q) = %s, want %s", tc.body, got, tc.want)
			}
		}
	})

	sha := func(c byte) string { return strings.Repeat(string(c), 40) }
	korean := Commit{SHA: sha('a'), Message: "feat: 로그인 추가\n\n토큰이 만료되면 다시 받는다.\n\nCo-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"}
	trailersOnly := Commit{SHA: sha('b'), Message: "fix(api): 토큰 만료 처리\n\nSigned-off-by: LADO <lado@example.com>"}
	englishBody := Commit{SHA: sha('c'), Message: "fix: 로그인 시간 초과 수정\n\nThe login page now waits for the token before it redirects."}
	short := Commit{SHA: sha('d'), Message: "chore: bump deps"}
	english := Commit{SHA: sha('e'), Message: "Add login page"}
	commit := func(c Commit, result string) domain.LadoVerifyCommit {
		subject, _, _ := strings.Cut(c.Message, "\n")
		return domain.LadoVerifyCommit{SHA: c.SHA, Subject: subject, Result: result}
	}
	title := func(text, result string) domain.LadoVerifyText {
		return domain.LadoVerifyText{Text: text, Result: result}
	}

	t.Run("not required without the validator", func(t *testing.T) {
		checkCommits(t, nil, []Commit{english}, "Add login page", domain.LadoVerifyInfo, domain.LadoCodeNotRequired, nil)
	})

	t.Run("Korean commits pass", func(t *testing.T) {
		checkCommits(t, &cfg, []Commit{korean, trailersOnly}, "feat: 로그인 추가", domain.LadoVerifyPass, "", domain.LadoCommitMessagesDetails{
			Commits: []domain.LadoVerifyCommit{commit(korean, MessageOK), commit(trailersOnly, MessageOK)},
			PRTitle: title("feat: 로그인 추가", MessageOK),
		})
	})

	t.Run("an English body or a short subject needs confirming", func(t *testing.T) {
		checkCommits(t, &cfg, []Commit{korean, englishBody, short}, "feat: 로그인 추가", domain.LadoVerifyConfirm, domain.LadoCodeCommitMessagesUnclear, domain.LadoCommitMessagesDetails{
			Commits: []domain.LadoVerifyCommit{commit(korean, MessageOK), commit(englishBody, MessageBodyUnclear), commit(short, MessageUnclear)},
			PRTitle: title("feat: 로그인 추가", MessageOK),
		})
	})

	t.Run("an English commit fails", func(t *testing.T) {
		checkCommits(t, &cfg, []Commit{short, english}, "feat: 로그인 추가", domain.LadoVerifyFail, domain.LadoCodeCommitMessagesFailed, domain.LadoCommitMessagesDetails{
			Commits: []domain.LadoVerifyCommit{commit(short, MessageUnclear), commit(english, MessageNotKorean)},
			PRTitle: title("feat: 로그인 추가", MessageOK),
		})
	})

	t.Run("an English PR title fails", func(t *testing.T) {
		checkCommits(t, &cfg, []Commit{korean}, "Add the login page", domain.LadoVerifyFail, domain.LadoCodeCommitMessagesFailed, domain.LadoCommitMessagesDetails{
			Commits: []domain.LadoVerifyCommit{commit(korean, MessageOK)},
			PRTitle: title("Add the login page", MessageNotKorean),
		})
	})

	t.Run("no commits pass", func(t *testing.T) {
		// Nothing lands on the base branch, so no commit takes the title.
		checkCommits(t, &cfg, nil, "Add the login page", domain.LadoVerifyPass, "", domain.LadoCommitMessagesDetails{
			PRTitle: title("Add the login page", MessageNotKorean),
		})
	})

	t.Run("another language is refused", func(t *testing.T) {
		other := domain.LadoCommitLocaleConfig{Language: "ja", AllowPrefixes: cfg.AllowPrefixes}
		if _, err := EvaluateCommits(&other, []Commit{korean}, "feat: 로그인 추가"); err == nil {
			t.Fatal("EvaluateCommits with language ja = nil error")
		}
	})
}

// PRD gate 9: a commit-msg hook refuses English messages, but a commit made
// with --no-verify skips it; LADO reads the commit from git after the
// merge-base and fails it all the same.
func TestNoVerifyCommitIsJudged(t *testing.T) {
	r := newTestRepo(t, t0.Add(-time.Hour))
	hook := filepath.Join(r.dir, ".git", "hooks", "commit-msg")
	// A message with no byte outside printable ASCII is taken as English.
	script := "#!/bin/sh\nif LC_ALL=C grep -q '[^ -~]' \"$1\"; then exit 0; fi\necho 'Write the commit message in Korean.' >&2\nexit 1\n"
	if err := os.MkdirAll(filepath.Dir(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hook, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	base := r.git(t0, r.dir, "rev-parse", "refs/remotes/origin/main")

	// The hook lets a Korean message through and refuses the English one.
	r.write(r.dir, "login.go", "package login\n")
	r.git(t0, r.dir, "add", "-A")
	r.git(t0.Add(time.Minute), r.dir, "commit", "-q", "-m", "feat: 로그인 추가")
	r.write(r.dir, "feature.go", "package feature\n")
	r.git(t0, r.dir, "add", "-A")
	refused := exec.Command("git", "-C", r.dir, "commit", "-q", "-m", "Add the feature")
	var stderr bytes.Buffer
	refused.Stderr = &stderr
	if err := refused.Run(); err == nil || !strings.Contains(stderr.String(), "Write the commit message in Korean.") {
		t.Fatalf("the commit-msg hook did not refuse the English message: %v\n%s", err, stderr.String())
	}
	r.git(t0.Add(2*time.Minute), r.dir, "commit", "-q", "--no-verify", "-m", "Add the feature")
	head := r.git(t0, r.dir, "rev-parse", "HEAD")

	commits, err := ExecGit{}.Commits(context.Background(), r.dir, base, head)
	if err != nil {
		t.Fatalf("Commits = %v", err)
	}
	if len(commits) != 2 || commits[1].SHA != head || commits[1].Message != "Add the feature" {
		t.Fatalf("Commits = %+v, want the Korean commit and then %s", commits, head)
	}
	cfg := domain.DefaultLadoCommitLocaleConfig()
	checkCommits(t, &cfg, commits, "feat: 기능 추가", domain.LadoVerifyFail, domain.LadoCodeCommitMessagesFailed, domain.LadoCommitMessagesDetails{
		Commits: []domain.LadoVerifyCommit{
			{SHA: commits[0].SHA, Subject: "feat: 로그인 추가", Result: MessageOK},
			{SHA: head, Subject: "Add the feature", Result: MessageNotKorean},
		},
		PRTitle: domain.LadoVerifyText{Text: "feat: 기능 추가", Result: MessageOK},
	})
}
