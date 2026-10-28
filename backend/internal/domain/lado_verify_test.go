package domain_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ladoVerifyHead is the PR head of the fixed verification record.
var ladoVerifyHead = strings.Repeat("a", 40)

// fixedLadoVerifyDetails are the details of the fixed record, one per kind in
// record order, with a few lists left nil.
func fixedLadoVerifyDetails() []any {
	exit0, exit1 := 0, 1
	return []any{
		domain.LadoRequiredChecksDetails{
			Required:    []string{"build"},
			RulesetRead: true,
			Checks:      []domain.LadoVerifyCheck{{Name: "build", Status: "passed", Required: true}, {Name: "lint", Status: "failed"}},
		},
		domain.LadoCommandsDetails{Runs: []domain.LadoVerifyCommandRun{
			{Name: "unit", CheckRunID: "lado-check-1", Status: "passed", ExitCode: &exit0, DurationMS: 1200},
			{Name: "e2e", CheckRunID: "lado-check-2", Status: "failed", ExitCode: &exit1, DurationMS: 5300},
			{Name: "lint", Status: "running"},
		}},
		domain.LadoCommitMessagesDetails{
			Commits: []domain.LadoVerifyCommit{{SHA: strings.Repeat("b", 40), Subject: "feat: 로그인 <beta> & 세션 추가", Result: "ok"}},
			PRTitle: domain.LadoVerifyText{Text: "wip", Result: "unclear"},
		},
		domain.LadoPathsDetails{OutsidePlan: []string{"docs/notes.md"}, OutsideCount: 1},
		domain.LadoBaseBranchDetails{Branch: "main", Remote: "origin"},
		domain.LadoProtectionDetails{Provider: "github", Repo: "acme/demo", Branch: "main", Missing: []string{"pull_request", "required_status_checks"}},
		nil,
		domain.LadoEvidenceDetails{FileVersion: 2, FileSHA256: strings.Repeat("e", 64), FileHead: ladoVerifyHead, Criteria: []domain.LadoEvidenceCriterion{
			{Index: 1, State: domain.LadoEvidenceLinked, Refs: []domain.LadoEvidenceRef{{Kind: domain.LadoEvidenceRefPRCheck, Key: "build", SHA: ladoVerifyHead}}},
			{Index: 2, State: domain.LadoEvidenceInferred},
		}},
	}
}

// fixedLadoVerification is a record with every kind: a failing command, two
// items to confirm, and two info items.
func fixedLadoVerification(t *testing.T, details []any) domain.LadoVerification {
	t.Helper()
	outcomes := []struct {
		outcome domain.LadoVerifyOutcome
		code    string
	}{
		{domain.LadoVerifyPass, ""},
		{domain.LadoVerifyFail, domain.LadoCodeCommandsFailed},
		{domain.LadoVerifyConfirm, domain.LadoCodeCommitMessagesUnclear},
		{domain.LadoVerifyConfirm, domain.LadoCodeOutsidePlan},
		{domain.LadoVerifyPass, domain.LadoCodeNoLocalBranch},
		{domain.LadoVerifyConfirm, domain.LadoCodeUnprotected},
		{domain.LadoVerifyInfo, domain.LadoCodeNotRequired},
		{domain.LadoVerifyInfo, ""},
	}
	v := domain.LadoVerification{
		ExecutionSessionID: "demo-7",
		HeadSHA:            ladoVerifyHead,
		PRURL:              "https://github.com/acme/demo/pull/8",
		Notified:           []string{string(domain.LadoVerifyCommands)},
		ComputedAt:         time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC),
	}
	for i, kind := range domain.LadoVerifyKinds() {
		item, err := domain.NewLadoVerifyItem(kind, outcomes[i].outcome, outcomes[i].code, details[i])
		if err != nil {
			t.Fatalf("NewLadoVerifyItem(%s): %v", kind, err)
		}
		v.Items = append(v.Items, item)
	}
	return v
}

func ladoVerifySHA(t *testing.T, v domain.LadoVerification) string {
	t.Helper()
	sum, err := v.SHA256()
	if err != nil {
		t.Fatalf("SHA256: %v", err)
	}
	return sum
}

func TestLadoVerificationContentIsTheFormat(t *testing.T) {
	v := fixedLadoVerification(t, fixedLadoVerifyDetails())
	// The format is fixed: integration approvals sign this hash. A change
	// here needs a new LadoVerifyFormat.
	want := `{"format":"lado-verify/1","prUrl":"https://github.com/acme/demo/pull/8","headSha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","items":[` +
		`{"kind":"required_checks","outcome":"pass","code":"","details":{"required":["build"],"rulesetRead":true,"checks":[{"name":"build","status":"passed","required":true},{"name":"lint","status":"failed","required":false}]}},` +
		`{"kind":"commands","outcome":"fail","code":"commands_failed","details":{"runs":[{"name":"unit","commandSha256":"","checkRunId":"lado-check-1","status":"passed","exitCode":0,"durationMs":1200},{"name":"e2e","commandSha256":"","checkRunId":"lado-check-2","status":"failed","exitCode":1,"durationMs":5300},{"name":"lint","commandSha256":"","checkRunId":"","status":"running","exitCode":null,"durationMs":0}]}},` +
		`{"kind":"commit_messages","outcome":"confirm","code":"commit_messages_unclear","details":{"commits":[{"sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","subject":"feat: 로그인 <beta> & 세션 추가","result":"ok"}],"prTitle":{"text":"wip","result":"unclear"}}},` +
		`{"kind":"paths","outcome":"confirm","code":"outside_plan","details":{"protected":[],"outsidePlan":["docs/notes.md"],"outsideCount":1}},` +
		`{"kind":"base_branch","outcome":"pass","code":"no_local_branch","details":{"branch":"main","remote":"origin","moved":[],"alreadyOnBase":false,"dispatchCommit":"","deleted":false}},` +
		`{"kind":"protection","outcome":"confirm","code":"unprotected","details":{"provider":"github","repo":"acme/demo","branch":"main","missing":["pull_request","required_status_checks"],"requiredChecks":[],"errorClass":""}},` +
		`{"kind":"review","outcome":"info","code":"not_required","details":{}},` +
		`{"kind":"evidence","outcome":"info","code":"","details":{"fileVersion":2,"fileSha256":"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee","fileHead":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","criteria":[{"index":1,"state":"linked","refs":[{"kind":"pr_check","key":"build","sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]},{"index":2,"state":"inferred","refs":[]}]}}]}`
	content, err := v.Content()
	if err != nil {
		t.Fatalf("Content: %v", err)
	}
	if string(content) != want {
		t.Fatalf("Content =\n%s\nwant\n%s", content, want)
	}
	const wantSHA = "13c8fd899ec18cbd14d62756d40f53903f73fa31b95f1112bb01e905b76f12d3"
	sum := sha256.Sum256([]byte(want))
	if got := ladoVerifySHA(t, v); got != hex.EncodeToString(sum[:]) || got != wantSHA {
		t.Fatalf("SHA256 = %s, want %s, the sha256 of the content", got, wantSHA)
	}

	// Times, the notified items and the execution are not the content.
	same := v
	same.ComputedAt = same.ComputedAt.Add(time.Hour)
	same.Notified = []string{string(domain.LadoVerifyCommands), string(domain.LadoVerifyPaths)}
	same.ExecutionSessionID = "demo-8"
	if got := ladoVerifySHA(t, same); got != wantSHA {
		t.Fatalf("SHA256 after editing times and notified items = %s, want %s", got, wantSHA)
	}
	// The stored form reads back to the same content.
	var stored struct {
		Items []domain.LadoVerifyItem `json:"items"`
	}
	if err := json.Unmarshal(content, &stored); err != nil {
		t.Fatalf("decode content: %v", err)
	}
	reread := domain.LadoVerification{HeadSHA: v.HeadSHA, PRURL: v.PRURL, Items: stored.Items}
	if got := ladoVerifySHA(t, reread); got != wantSHA {
		t.Fatalf("SHA256 of the decoded content = %s, want %s", got, wantSHA)
	}

	// Any change to what the developer saw changes the hash.
	for name, edit := range map[string]func(*domain.LadoVerification, []any){
		"one character of a detail": func(_ *domain.LadoVerification, details []any) {
			d := details[2].(domain.LadoCommitMessagesDetails)
			d.Commits = []domain.LadoVerifyCommit{{SHA: strings.Repeat("b", 40), Subject: "feat: 로그인 <beta> & 세션 추기", Result: "ok"}}
			details[2] = d
		},
		"details given instead of none": func(_ *domain.LadoVerification, details []any) {
			details[6] = domain.LadoReviewDetails{}
		},
		"the head": func(v *domain.LadoVerification, _ []any) { v.HeadSHA = strings.Repeat("c", 40) },
		"the PR":   func(v *domain.LadoVerification, _ []any) { v.PRURL = "https://github.com/acme/demo/pull/9" },
		"an outcome": func(v *domain.LadoVerification, _ []any) {
			v.Items[3].Outcome = domain.LadoVerifyPass
		},
		"a code": func(v *domain.LadoVerification, _ []any) { v.Items[5].Code = domain.LadoCodeProtectionUnknown },
		"the item order": func(v *domain.LadoVerification, _ []any) {
			v.Items[0], v.Items[1] = v.Items[1], v.Items[0]
		},
	} {
		details := fixedLadoVerifyDetails()
		edited := v
		edited.Items = slices.Clone(v.Items)
		edit(&edited, details)
		if !reflect.DeepEqual(details, fixedLadoVerifyDetails()) {
			edited = fixedLadoVerification(t, details)
		}
		if got := ladoVerifySHA(t, edited); got == wantSHA {
			t.Errorf("editing %s kept the hash", name)
		}
	}

	// Empty lists are [], never null.
	empty, err := domain.LadoVerification{HeadSHA: ladoVerifyHead, PRURL: v.PRURL}.Content()
	if err != nil || string(empty) != `{"format":"lado-verify/1","prUrl":"https://github.com/acme/demo/pull/8","headSha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","items":[]}` {
		t.Fatalf("Content without items = %s (%v), want items []", empty, err)
	}
	for _, tc := range []struct {
		kind    domain.LadoVerifyKind
		details any
		want    string
	}{
		{domain.LadoVerifyRequiredChecks, domain.LadoRequiredChecksDetails{}, `{"required":[],"rulesetRead":false,"checks":[]}`},
		{domain.LadoVerifyCommands, domain.LadoCommandsDetails{}, `{"runs":[]}`},
		{domain.LadoVerifyCommitMessages, domain.LadoCommitMessagesDetails{}, `{"commits":[],"prTitle":{"text":"","result":""}}`},
		{domain.LadoVerifyPaths, domain.LadoPathsDetails{}, `{"protected":[],"outsidePlan":[],"outsideCount":0}`},
		{domain.LadoVerifyBaseBranch, domain.LadoBaseBranchDetails{}, `{"branch":"","remote":"","moved":[],"alreadyOnBase":false,"dispatchCommit":"","deleted":false}`},
		{domain.LadoVerifyProtection, domain.LadoProtectionDetails{}, `{"provider":"","repo":"","branch":"","missing":[],"requiredChecks":[],"errorClass":""}`},
		{domain.LadoVerifyReview, domain.LadoReviewDetails{}, `{"rounds":0,"runId":"","verdict":"","harness":"","note":""}`},
		{domain.LadoVerifyEvidence, domain.LadoEvidenceDetails{Criteria: []domain.LadoEvidenceCriterion{{Index: 1, State: domain.LadoEvidenceClaimed}}}, `{"fileVersion":0,"fileSha256":"","fileHead":"","criteria":[{"index":1,"state":"claimed","refs":[]}]}`},
		{domain.LadoVerifyEvidence, domain.LadoEvidenceDetails{}, `{"fileVersion":0,"fileSha256":"","fileHead":"","criteria":[]}`},
	} {
		item, err := domain.NewLadoVerifyItem(tc.kind, domain.LadoVerifyInfo, "", tc.details)
		if err != nil || string(item.Details) != tc.want {
			t.Errorf("%s details = %s (%v), want %s", tc.kind, item.Details, err, tc.want)
		}
	}
}

func TestNewLadoVerifyItemRefusesMistakes(t *testing.T) {
	for name, build := range map[string]func() (domain.LadoVerifyItem, error){
		"unknown kind": func() (domain.LadoVerifyItem, error) {
			return domain.NewLadoVerifyItem("lint", domain.LadoVerifyPass, "", nil)
		},
		"unknown outcome": func() (domain.LadoVerifyItem, error) {
			return domain.NewLadoVerifyItem(domain.LadoVerifyPaths, "ok", "", nil)
		},
		"wait without a code": func() (domain.LadoVerifyItem, error) {
			return domain.NewLadoVerifyItem(domain.LadoVerifyRequiredChecks, domain.LadoVerifyWait, "", nil)
		},
		"fail without a code": func() (domain.LadoVerifyItem, error) {
			return domain.NewLadoVerifyItem(domain.LadoVerifyCommands, domain.LadoVerifyFail, "", nil)
		},
		"confirm without a code": func() (domain.LadoVerifyItem, error) {
			return domain.NewLadoVerifyItem(domain.LadoVerifyProtection, domain.LadoVerifyConfirm, "", nil)
		},
		"details of another kind": func() (domain.LadoVerifyItem, error) {
			return domain.NewLadoVerifyItem(domain.LadoVerifyCommands, domain.LadoVerifyPass, "", domain.LadoPathsDetails{})
		},
		"details by pointer": func() (domain.LadoVerifyItem, error) {
			return domain.NewLadoVerifyItem(domain.LadoVerifyPaths, domain.LadoVerifyPass, "", &domain.LadoPathsDetails{})
		},
		"details that are no struct of a kind": func() (domain.LadoVerifyItem, error) {
			return domain.NewLadoVerifyItem(domain.LadoVerifyReview, domain.LadoVerifyPass, "", map[string]string{"verdict": "approved"})
		},
	} {
		if item, err := build(); err == nil {
			t.Errorf("%s: NewLadoVerifyItem = %+v, want an error", name, item)
		}
	}
	criteria := []domain.LadoEvidenceCriterion{{Index: 1, State: domain.LadoEvidenceInferred}}
	if _, err := domain.NewLadoVerifyItem(domain.LadoVerifyEvidence, domain.LadoVerifyInfo, "", domain.LadoEvidenceDetails{Criteria: criteria}); err != nil {
		t.Fatalf("NewLadoVerifyItem(evidence): %v", err)
	}
	if criteria[0].Refs != nil {
		t.Fatalf("NewLadoVerifyItem changed the caller's criteria: %+v", criteria)
	}
	if got := domain.LadoVerifyKinds(); len(got) != 8 || got[0] != domain.LadoVerifyRequiredChecks || got[7] != domain.LadoVerifyEvidence {
		t.Fatalf("LadoVerifyKinds = %v, want the 8 kinds in record order", got)
	}
}

// ladoVerifyItems builds a record of items with the given outcomes and
// codes, all of kind paths: Overall and ConfirmCodes read only those.
func ladoVerifyItems(outcomes ...string) domain.LadoVerification {
	var v domain.LadoVerification
	for _, oc := range outcomes {
		outcome, code, _ := strings.Cut(oc, ":")
		v.Items = append(v.Items, domain.LadoVerifyItem{Kind: domain.LadoVerifyPaths, Outcome: domain.LadoVerifyOutcome(outcome), Code: code, Details: json.RawMessage("{}")})
	}
	return v
}

func TestLadoVerificationOverall(t *testing.T) {
	for _, tc := range []struct {
		name    string
		items   []string
		outcome domain.LadoVerifyOutcome
		reason  string
	}{
		{"no items", nil, domain.LadoVerifyPass, ""},
		{"all pass", []string{"pass:", "pass:none_reported"}, domain.LadoVerifyPass, ""},
		{"info is ignored", []string{"info:not_required", "pass:", "info:"}, domain.LadoVerifyPass, ""},
		{"only info", []string{"info:not_required"}, domain.LadoVerifyPass, ""},
		{"confirm over pass", []string{"pass:", "confirm:outside_plan", "info:"}, domain.LadoVerifyConfirm, ""},
		{"wait over confirm", []string{"confirm:outside_plan", "wait:review_pending"}, domain.LadoVerifyWait, domain.LadoCodeReviewPending},
		{"first wait", []string{"wait:checks_pending", "confirm:unprotected", "wait:commands_running"}, domain.LadoVerifyWait, domain.LadoCodeChecksPending},
		{"fail over wait", []string{"wait:checks_pending", "fail:commands_failed"}, domain.LadoVerifyFail, domain.LadoCodeCommandsFailed},
		{"first fail", []string{"confirm:base_moved", "fail:protected_path", "wait:review_pending", "fail:changes_requested"}, domain.LadoVerifyFail, domain.LadoCodeProtectedPath},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outcome, reason := ladoVerifyItems(tc.items...).Overall()
			if outcome != tc.outcome || reason != tc.reason {
				t.Fatalf("Overall = %s, %q; want %s, %q", outcome, reason, tc.outcome, tc.reason)
			}
		})
	}
}

func TestLadoVerificationConfirmCodes(t *testing.T) {
	v := ladoVerifyItems("pass:none_reported", "confirm:unprotected", "wait:review_pending", "confirm:outside_plan",
		"fail:commit_messages_failed", "confirm:base_moved", "info:not_required", "confirm:outside_plan")
	want := []string{domain.LadoCodeBaseMoved, domain.LadoCodeOutsidePlan, domain.LadoCodeUnprotected}
	if got := v.ConfirmCodes(); !slices.Equal(got, want) {
		t.Fatalf("ConfirmCodes = %v, want %v", got, want)
	}
	if got := ladoVerifyItems("pass:", "wait:checks_pending").ConfirmCodes(); got == nil || len(got) != 0 {
		t.Fatalf("ConfirmCodes without confirm items = %#v, want an empty list", got)
	}
}

func TestValidateLadoCheckCommands(t *testing.T) {
	valid := func(n int) []domain.LadoCheckCommand {
		commands := make([]domain.LadoCheckCommand, n)
		for i := range commands {
			commands[i] = domain.LadoCheckCommand{Name: "check " + string(rune('a'+i)), Command: "npm test"}
		}
		return commands
	}
	with := func(i int, edit func(*domain.LadoCheckCommand)) []domain.LadoCheckCommand {
		commands := valid(3)
		edit(&commands[i])
		return commands
	}
	for _, tc := range []struct {
		name     string
		commands []domain.LadoCheckCommand
		want     *domain.LadoCheckCommandProblem
	}{
		{"no commands", nil, nil},
		{"ten commands", valid(10), nil},
		{"names with spaces, dots, underscores and dashes", []domain.LadoCheckCommand{
			{Name: "Go vet", Command: "go vet ./..."}, {Name: "lint.v2_fast-mode", Command: "npm run lint"},
			{Name: "9", Command: "make test"}, {Name: strings.Repeat("n", 40), Command: strings.Repeat("x", 2000)},
		}, nil},
		{"eleven commands", valid(11), &domain.LadoCheckCommandProblem{Index: 10, Reason: domain.LadoCheckCommandTooMany}},
		{"empty name", with(1, func(c *domain.LadoCheckCommand) { c.Name = "" }), &domain.LadoCheckCommandProblem{Index: 1, Field: "name", Reason: domain.LadoCheckCommandRequired}},
		{"41-character name", with(2, func(c *domain.LadoCheckCommand) { c.Name = strings.Repeat("n", 41) }), &domain.LadoCheckCommandProblem{Index: 2, Field: "name", Reason: domain.LadoCheckCommandTooLong}},
		{"name starting with a dash", with(0, func(c *domain.LadoCheckCommand) { c.Name = "-lint" }), &domain.LadoCheckCommandProblem{Index: 0, Field: "name", Reason: domain.LadoCheckCommandInvalid}},
		{"name starting with a space", with(0, func(c *domain.LadoCheckCommand) { c.Name = " lint" }), &domain.LadoCheckCommandProblem{Index: 0, Field: "name", Reason: domain.LadoCheckCommandInvalid}},
		{"name with a slash", with(1, func(c *domain.LadoCheckCommand) { c.Name = "unit/fast" }), &domain.LadoCheckCommandProblem{Index: 1, Field: "name", Reason: domain.LadoCheckCommandInvalid}},
		{"name in Hangul", with(1, func(c *domain.LadoCheckCommand) { c.Name = "검사" }), &domain.LadoCheckCommandProblem{Index: 1, Field: "name", Reason: domain.LadoCheckCommandInvalid}},
		{"duplicate name", with(2, func(c *domain.LadoCheckCommand) { c.Name = "check a" }), &domain.LadoCheckCommandProblem{Index: 2, Field: "name", Reason: domain.LadoCheckCommandDuplicate}},
		{"duplicate name in another case", with(1, func(c *domain.LadoCheckCommand) { c.Name = "CHECK A" }), &domain.LadoCheckCommandProblem{Index: 1, Field: "name", Reason: domain.LadoCheckCommandDuplicate}},
		{"empty command", with(1, func(c *domain.LadoCheckCommand) { c.Command = "" }), &domain.LadoCheckCommandProblem{Index: 1, Field: "command", Reason: domain.LadoCheckCommandRequired}},
		{"blank command", with(1, func(c *domain.LadoCheckCommand) { c.Command = " \t\n" }), &domain.LadoCheckCommandProblem{Index: 1, Field: "command", Reason: domain.LadoCheckCommandRequired}},
		{"2001-character command", with(0, func(c *domain.LadoCheckCommand) { c.Command = strings.Repeat("x", 2001) }), &domain.LadoCheckCommandProblem{Index: 0, Field: "command", Reason: domain.LadoCheckCommandTooLong}},
		{"command with NUL", with(2, func(c *domain.LadoCheckCommand) { c.Command = "npm test\x00 --watch" }), &domain.LadoCheckCommandProblem{Index: 2, Field: "command", Reason: domain.LadoCheckCommandInvalid}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := domain.ValidateLadoCheckCommands(tc.commands); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ValidateLadoCheckCommands = %+v, want %+v", got, tc.want)
			}
		})
	}
	// The command counts characters, not bytes.
	if got := domain.ValidateLadoCheckCommands([]domain.LadoCheckCommand{{Name: "echo", Command: strings.Repeat("검", 2000)}}); got != nil {
		t.Fatalf("a 2000-character Hangul command = %+v, want no problem", got)
	}
	data, err := json.Marshal(domain.LadoCheckCommand{Name: "unit", Command: "go test ./..."})
	if err != nil || string(data) != `{"name":"unit","command":"go test ./..."}` {
		t.Fatalf("LadoCheckCommand JSON = %s (%v)", data, err)
	}
	if domain.LadoCheckTimeoutMin != 5 || domain.LadoCheckTimeoutMax != 120 || domain.LadoCheckTimeoutDefault != 30 || domain.LadoCheckCommandsMax != 10 {
		t.Fatal("check command limits changed")
	}
}
