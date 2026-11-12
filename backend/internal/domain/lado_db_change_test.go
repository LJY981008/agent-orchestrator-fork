package domain_test

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestLadoDBChangePhaseOf(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		want    domain.LadoDBChangePhase
	}{
		{"empty", "", domain.LadoDBChangePreMerge},
		{"plain SQL", "ALTER TABLE users ADD COLUMN age int;\n", domain.LadoDBChangePreMerge},
		{"marker first", "-- lado:after-deploy\nDROP TABLE old;\n", domain.LadoDBChangeAfterDeploy},
		{"marker after blank lines", "\n  \r\n\t-- LADO:After-Deploy  \r\nDROP TABLE old;", domain.LadoDBChangeAfterDeploy},
		{"marker after a BOM", "\ufeff-- lado:after-deploy\nDROP TABLE old;", domain.LadoDBChangeAfterDeploy},
		{"marker without a line end", "-- lado:after-deploy", domain.LadoDBChangeAfterDeploy},
		{"marker on the second line", "-- drops the old table\n-- lado:after-deploy\nDROP TABLE old;", domain.LadoDBChangePreMerge},
		{"marker with more text", "-- lado:after-deploy please\nDROP TABLE old;", domain.LadoDBChangePreMerge},
		{"marker inside a statement", "SELECT 1; -- lado:after-deploy", domain.LadoDBChangePreMerge},
	} {
		if got := domain.LadoDBChangePhaseOf(tc.content); got != tc.want {
			t.Errorf("%s: LadoDBChangePhaseOf = %s, want %s", tc.name, got, tc.want)
		}
	}
}

// validLadoDBChangeSettings are settings ValidateLadoDBChangeSettings
// accepts.
func validLadoDBChangeSettings() domain.LadoDBChangeSettings {
	return domain.LadoDBChangeSettings{
		ProjectID:      "demo",
		Repo:           "back",
		Enabled:        true,
		Paths:          []string{"scripts/migrations/*.sql", "db/**/*.sql"},
		Command:        `psql "$LADO_DB_DSN" -v ON_ERROR_STOP=1 -f {file}`,
		TimeoutMinutes: 10,
		SecretNames:    []string{"LADO_DB_DSN", "PGPASSWORD"},
	}
}

func TestValidateLadoDBChangeSettingsAccepts(t *testing.T) {
	for name, edit := range map[string]func(*domain.LadoDBChangeSettings){
		"as they are":                func(*domain.LadoDBChangeSettings) {},
		"a single-repository root":   func(s *domain.LadoDBChangeSettings) { s.Repo = "" },
		"a nested child repository":  func(s *domain.LadoDBChangeSettings) { s.Repo = "services/api" },
		"the default time limit":     func(s *domain.LadoDBChangeSettings) { s.TimeoutMinutes = 0 },
		"the shortest time limit":    func(s *domain.LadoDBChangeSettings) { s.TimeoutMinutes = 1 },
		"the longest time limit":     func(s *domain.LadoDBChangeSettings) { s.TimeoutMinutes = 120 },
		"no secrets":                 func(s *domain.LadoDBChangeSettings) { s.SecretNames = nil },
		"ten paths":                  func(s *domain.LadoDBChangeSettings) { s.Paths = ladoDBChangeGlobs(10) },
		"ten secrets":                func(s *domain.LadoDBChangeSettings) { s.SecretNames = ladoDBChangeSecretNames(10) },
		"a command of 2000 runes":    func(s *domain.LadoDBChangeSettings) { s.Command = "psql -f {file} " + strings.Repeat("가", 2000-15) },
		"a name of 64 characters":    func(s *domain.LadoDBChangeSettings) { s.SecretNames = []string{"_" + strings.Repeat("A", 63)} },
		"a file named ..sql":         func(s *domain.LadoDBChangeSettings) { s.Paths = []string{"db/..sql"} },
		"turned off and left empty":  func(s *domain.LadoDBChangeSettings) { s.Enabled, s.Paths, s.Command = false, nil, "" },
		"turned off with its values": func(s *domain.LadoDBChangeSettings) { s.Enabled = false },
	} {
		s := validLadoDBChangeSettings()
		edit(&s)
		if problems := domain.ValidateLadoDBChangeSettings(s); problems != nil {
			t.Errorf("%s: problems = %+v, want none", name, problems)
		}
	}
}

func ladoDBChangeGlobs(n int) []string {
	globs := make([]string, n)
	for i := range globs {
		globs[i] = fmt.Sprintf("db/%d/*.sql", i)
	}
	return globs
}

func ladoDBChangeSecretNames(n int) []string {
	names := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf("DB_SECRET_%d", i)
	}
	return names
}

func TestValidateLadoDBChangeSettingsRefuses(t *testing.T) {
	problem := func(field, reason string) []domain.LadoDBChangeProblem {
		return []domain.LadoDBChangeProblem{{Field: field, Reason: reason}}
	}
	for name, tc := range map[string]struct {
		edit func(*domain.LadoDBChangeSettings)
		want []domain.LadoDBChangeProblem
	}{
		"a repository outside the workspace": {func(s *domain.LadoDBChangeSettings) { s.Repo = "../other" }, problem("repo", domain.LadoDBChangeProblemInvalid)},
		"an absolute repository":             {func(s *domain.LadoDBChangeSettings) { s.Repo = "/srv/back" }, problem("repo", domain.LadoDBChangeProblemInvalid)},
		"no paths":                           {func(s *domain.LadoDBChangeSettings) { s.Paths = nil }, problem("paths", domain.LadoDBChangeProblemRequired)},
		"eleven paths":                       {func(s *domain.LadoDBChangeSettings) { s.Paths = ladoDBChangeGlobs(11) }, problem("paths", domain.LadoDBChangeProblemTooMany)},
		"an empty path":                      {func(s *domain.LadoDBChangeSettings) { s.Paths = []string{" "} }, problem("paths[0]", domain.LadoDBChangeProblemRequired)},
		"a parent folder":                    {func(s *domain.LadoDBChangeSettings) { s.Paths = []string{"db/../../etc/*.sql"} }, problem("paths[0]", domain.LadoDBChangeProblemParentDir)},
		"a leading parent folder":            {func(s *domain.LadoDBChangeSettings) { s.Paths = []string{"../*.sql"} }, problem("paths[0]", domain.LadoDBChangeProblemParentDir)},
		"an absolute path":                   {func(s *domain.LadoDBChangeSettings) { s.Paths = []string{"/etc/*.sql"} }, problem("paths[0]", domain.LadoDBChangeProblemAbsolute)},
		"a drive path":                       {func(s *domain.LadoDBChangeSettings) { s.Paths = []string{`C:/db/*.sql`} }, problem("paths[0]", domain.LadoDBChangeProblemAbsolute)},
		"a backslash":                        {func(s *domain.LadoDBChangeSettings) { s.Paths = []string{`db\*.sql`} }, problem("paths[0]", domain.LadoDBChangeProblemInvalid)},
		"spaces around a path":               {func(s *domain.LadoDBChangeSettings) { s.Paths = []string{" db/*.sql"} }, problem("paths[0]", domain.LadoDBChangeProblemInvalid)},
		"a line break in a path":             {func(s *domain.LadoDBChangeSettings) { s.Paths = []string{"db/*.sql\nx"} }, problem("paths[0]", domain.LadoDBChangeProblemControl)},
		"a long path":                        {func(s *domain.LadoDBChangeSettings) { s.Paths = []string{strings.Repeat("a", 257)} }, problem("paths[0]", domain.LadoDBChangeProblemTooLong)},
		"a duplicate path":                   {func(s *domain.LadoDBChangeSettings) { s.Paths = []string{"db/*.sql", "db/*.sql"} }, problem("paths[1]", domain.LadoDBChangeProblemDuplicate)},
		"no command":                         {func(s *domain.LadoDBChangeSettings) { s.Command = "  " }, problem("command", domain.LadoDBChangeProblemRequired)},
		"no {file}":                          {func(s *domain.LadoDBChangeSettings) { s.Command = "psql -f migration.sql" }, problem("command", domain.LadoDBChangeProblemNoPlaceholder)},
		"two lines":                          {func(s *domain.LadoDBChangeSettings) { s.Command = "psql -f {file}\nrm -rf /" }, problem("command", domain.LadoDBChangeProblemControl)},
		"a tab":                              {func(s *domain.LadoDBChangeSettings) { s.Command = "psql\t-f {file}" }, problem("command", domain.LadoDBChangeProblemControl)},
		"a bidirectional control":            {func(s *domain.LadoDBChangeSettings) { s.Command = "psql -f {file} \u202e" }, problem("command", domain.LadoDBChangeProblemBidi)},
		"a command of 2001 runes":            {func(s *domain.LadoDBChangeSettings) { s.Command = "psql -f {file} " + strings.Repeat("가", 2001-15) }, problem("command", domain.LadoDBChangeProblemTooLong)},
		"a negative time limit":              {func(s *domain.LadoDBChangeSettings) { s.TimeoutMinutes = -1 }, problem("timeoutMinutes", domain.LadoDBChangeProblemOutOfRange)},
		"a time limit over two hours":        {func(s *domain.LadoDBChangeSettings) { s.TimeoutMinutes = 121 }, problem("timeoutMinutes", domain.LadoDBChangeProblemOutOfRange)},
		"eleven secrets":                     {func(s *domain.LadoDBChangeSettings) { s.SecretNames = ladoDBChangeSecretNames(11) }, problem("secretNames", domain.LadoDBChangeProblemTooMany)},
		"a lower-case secret":                {func(s *domain.LadoDBChangeSettings) { s.SecretNames = []string{"db_dsn"} }, problem("secretNames[0]", domain.LadoDBChangeProblemInvalid)},
		"a secret starting with a digit":     {func(s *domain.LadoDBChangeSettings) { s.SecretNames = []string{"1DSN"} }, problem("secretNames[0]", domain.LadoDBChangeProblemInvalid)},
		"a secret with a dash":               {func(s *domain.LadoDBChangeSettings) { s.SecretNames = []string{"DB-DSN"} }, problem("secretNames[0]", domain.LadoDBChangeProblemInvalid)},
		"a secret of 65 characters":          {func(s *domain.LadoDBChangeSettings) { s.SecretNames = []string{strings.Repeat("A", 65)} }, problem("secretNames[0]", domain.LadoDBChangeProblemInvalid)},
		"an empty secret":                    {func(s *domain.LadoDBChangeSettings) { s.SecretNames = []string{""} }, problem("secretNames[0]", domain.LadoDBChangeProblemInvalid)},
		"PATH as a secret":                   {func(s *domain.LadoDBChangeSettings) { s.SecretNames = []string{"PATH"} }, problem("secretNames[0]", domain.LadoDBChangeProblemReservedSecret)},
		"a duplicate secret":                 {func(s *domain.LadoDBChangeSettings) { s.SecretNames = []string{"DSN", "DSN"} }, problem("secretNames[1]", domain.LadoDBChangeProblemDuplicate)},
		"several problems at once": {func(s *domain.LadoDBChangeSettings) {
			s.Paths, s.Command, s.SecretNames = []string{"../x"}, "psql", []string{"HOME"}
		}, []domain.LadoDBChangeProblem{
			{Field: "paths[0]", Reason: domain.LadoDBChangeProblemParentDir},
			{Field: "command", Reason: domain.LadoDBChangeProblemNoPlaceholder},
			{Field: "secretNames[0]", Reason: domain.LadoDBChangeProblemReservedSecret},
		}},
		// Turned off, the settings may be empty, but what they hold is
		// still checked.
		"turned off with a bad path": {func(s *domain.LadoDBChangeSettings) { s.Enabled, s.Paths = false, []string{"../x"} }, problem("paths[0]", domain.LadoDBChangeProblemParentDir)},
	} {
		s := validLadoDBChangeSettings()
		tc.edit(&s)
		if got := domain.ValidateLadoDBChangeSettings(s); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: problems = %+v, want %+v", name, got, tc.want)
		}
	}
	err := error(&domain.LadoDBChangeInvalidError{Problems: []domain.LadoDBChangeProblem{{Field: "command", Reason: domain.LadoDBChangeProblemRequired}}})
	var invalid *domain.LadoDBChangeInvalidError
	if !errors.As(err, &invalid) || !strings.Contains(err.Error(), "command: required") {
		t.Fatalf("LadoDBChangeInvalidError = %v", err)
	}
}

// The names LADO's runner withholds or sets, and the ones the shell needs,
// are refused as secrets without case; names that merely look like them are
// not.
func TestLadoDBChangeReservedEnv(t *testing.T) {
	for _, name := range []string{
		"AO_SESSION_ID", "ao_data_dir", "GCM_INTERACTIVE", "GH_HOST", "GITHUB_TOKEN", "GITLAB_HOST",
		"GIT_CONFIG_KEY_0", "GIT_CONFIG_VALUE_0", "SSH_ASKPASS", "GIT_ASKPASS", "DB_TOKEN", "OPENAI_API_KEY",
		"SSH_AUTH_SOCK", "GIT_SSH", "GIT_SSH_COMMAND", "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT", "GIT_TERMINAL_PROMPT",
		"PATH", "path", "HOME", "USERPROFILE", "SYSTEMROOT", "SystemRoot", "COMSPEC", "TEMP", "TMP", "TMPDIR", "GH_CONFIG_DIR",
	} {
		if !domain.LadoDBChangeReservedEnv(name) {
			t.Errorf("LadoDBChangeReservedEnv(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"LADO_DB_DSN", "PGPASSWORD", "PGHOST", "DB_TOKENS", "PATHS", "MY_HOME", "GIT_AUTHOR_NAME", "AOX"} {
		if domain.LadoDBChangeReservedEnv(name) {
			t.Errorf("LadoDBChangeReservedEnv(%q) = true, want false", name)
		}
	}
}

func TestLadoDBChangeSettingsSHA256(t *testing.T) {
	s := validLadoDBChangeSettings()
	base := s.SHA256()
	if len(base) != 64 {
		t.Fatalf("SHA256 = %q, want 64 hex characters", base)
	}
	// The times are no part of it; nil and empty lists are the same.
	same := s
	same.EnabledAt, same.UpdatedAt = time.Now(), time.Now()
	if same.SHA256() != base {
		t.Fatal("the times changed the settings hash")
	}
	empty, emptied := s, s
	empty.SecretNames, emptied.SecretNames = nil, []string{}
	if empty.SHA256() != emptied.SHA256() {
		t.Fatal("nil and empty secret names hash differently")
	}
	for name, edit := range map[string]func(*domain.LadoDBChangeSettings){
		"project":  func(s *domain.LadoDBChangeSettings) { s.ProjectID = "other" },
		"repo":     func(s *domain.LadoDBChangeSettings) { s.Repo = "front" },
		"enabled":  func(s *domain.LadoDBChangeSettings) { s.Enabled = false },
		"paths":    func(s *domain.LadoDBChangeSettings) { s.Paths = []string{"db/*.sql"} },
		"command":  func(s *domain.LadoDBChangeSettings) { s.Command += " -q" },
		"timeout":  func(s *domain.LadoDBChangeSettings) { s.TimeoutMinutes = 11 },
		"secrets":  func(s *domain.LadoDBChangeSettings) { s.SecretNames = []string{"LADO_DB_DSN"} },
		"baseline": func(s *domain.LadoDBChangeSettings) { s.BaselineCommit = strings.Repeat("c", 40) },
	} {
		changed := s
		edit(&changed)
		if changed.SHA256() == base {
			t.Errorf("a change of the %s kept the settings hash", name)
		}
	}
	other := s
	other.Repo = "front"
	if domain.LadoDBChangeSettingsListSHA256([]domain.LadoDBChangeSettings{s, other}) == domain.LadoDBChangeSettingsListSHA256([]domain.LadoDBChangeSettings{other, s}) ||
		domain.LadoDBChangeSettingsListSHA256(nil) == domain.LadoDBChangeSettingsListSHA256([]domain.LadoDBChangeSettings{s}) {
		t.Fatal("the list hash does not follow the settings")
	}
	if got := s.Timeout(); got != 10*time.Minute {
		t.Fatalf("Timeout = %s", got)
	}
	s.TimeoutMinutes = 0
	if got := s.Timeout(); got != domain.LadoDBChangeTimeoutDefault*time.Minute {
		t.Fatalf("default Timeout = %s", got)
	}
	update := domain.LadoDBChangeSettingsUpdate{Repo: "back", Enabled: true, Paths: []string{"db/*.sql"}, Command: "psql -f {file}", TimeoutMinutes: 5, SecretNames: []string{"DSN"}}
	if got := update.Settings("demo"); got.ProjectID != "demo" || got.Repo != "back" || !got.Enabled || got.Command != "psql -f {file}" || got.TimeoutMinutes != 5 || len(got.SecretNames) != 1 {
		t.Fatalf("Settings = %+v", got)
	}
}

// What blocks a merge for good stops LADO from applying SQL; what LADO's own
// missing lado/integration status causes does not.
func TestLadoDBChangeMergeStoppers(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   domain.LadoMergeReadiness
		want []string
	}{
		{"ready", domain.LadoMergeReadiness{Mergeability: domain.MergeMergeable}, nil},
		{"the missing status only", domain.LadoMergeReadiness{
			Blockers:     []string{domain.MergeBlockerCIPending, domain.MergeBlockerNotMergeable, domain.MergeBlockerHeadMismatch, domain.MergeBlockerReviewPartial},
			Mergeability: domain.MergeBlocked,
		}, nil},
		{"unknown CI", domain.LadoMergeReadiness{Blockers: []string{domain.MergeBlockerCIUnknown}, Mergeability: domain.MergeMergeable}, nil},
		{"no pipeline", domain.LadoMergeReadiness{Blockers: []string{domain.MergeBlockerCINoPipeline}, Mergeability: domain.MergeMergeable}, nil},
		{"unstable", domain.LadoMergeReadiness{Blockers: []string{domain.MergeBlockerNotMergeable}, Mergeability: domain.MergeUnstable}, nil},
		{"unknown mergeability", domain.LadoMergeReadiness{Blockers: []string{domain.MergeBlockerNotMergeable}, Mergeability: domain.MergeUnknown}, nil},
		{"a conflict", domain.LadoMergeReadiness{Blockers: []string{domain.MergeBlockerNotMergeable}, Mergeability: domain.MergeConflicting},
			[]string{domain.MergeBlockerNotMergeable}},
		{"unresolved comments", domain.LadoMergeReadiness{Blockers: []string{domain.MergeBlockerCIPending, domain.MergeBlockerUnresolvedComments}, Mergeability: domain.MergeBlocked},
			[]string{domain.MergeBlockerUnresolvedComments}},
		{"every stopper, in order", domain.LadoMergeReadiness{
			Blockers: []string{domain.MergeBlockerMerged, domain.MergeBlockerClosed, domain.MergeBlockerDraft, domain.MergeBlockerCIFailing,
				domain.MergeBlockerChangesRequested, domain.MergeBlockerUnresolvedComments, domain.MergeBlockerNotMergeable, domain.MergeBlockerNotMergeable},
			Mergeability: domain.MergeConflicting, Merged: true,
		}, []string{domain.MergeBlockerMerged, domain.MergeBlockerClosed, domain.MergeBlockerDraft, domain.MergeBlockerCIFailing,
			domain.MergeBlockerChangesRequested, domain.MergeBlockerUnresolvedComments, domain.MergeBlockerNotMergeable}},
		{"a review the host requires", domain.LadoMergeReadiness{
			Blockers: []string{domain.MergeBlockerCIPending, domain.MergeBlockerNotMergeable}, Mergeability: domain.MergeBlocked, Review: domain.ReviewRequired,
		}, []string{domain.LadoDBChangeStopperReviewRequired}},
		{"an approved or absent review", domain.LadoMergeReadiness{Mergeability: domain.MergeBlocked, Review: domain.ReviewApproved}, nil},
		{"review required after the other stoppers", domain.LadoMergeReadiness{
			Blockers: []string{domain.MergeBlockerDraft}, Review: domain.ReviewRequired,
		}, []string{domain.MergeBlockerDraft, domain.LadoDBChangeStopperReviewRequired}},
		{"merged without the blocker", domain.LadoMergeReadiness{Blockers: []string{domain.MergeBlockerDraft}, Merged: true},
			[]string{domain.MergeBlockerMerged, domain.MergeBlockerDraft}},
	} {
		if got := domain.LadoDBChangeMergeStoppers(tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: LadoDBChangeMergeStoppers = %v, want %v", tc.name, got, tc.want)
		}
	}
	// The rule reads what AO's own readiness reports.
	r := domain.MergeReadiness{CI: domain.CIPending, Review: domain.ReviewChangesRequest, Mergeability: domain.MergeBlocked}
	got := domain.LadoDBChangeMergeStoppers(domain.LadoMergeReadiness{Blockers: r.Blockers(), Mergeability: r.Mergeability})
	if !reflect.DeepEqual(got, []string{domain.MergeBlockerChangesRequested}) {
		t.Fatalf("stoppers of %+v = %v", r, got)
	}
}

func TestLadoDBChangeTypes(t *testing.T) {
	if domain.LadoDBChangeRunAfterDeploy.Phase() != domain.LadoDBChangeAfterDeploy || domain.LadoDBChangeRunPreMerge.Phase() != domain.LadoDBChangePreMerge {
		t.Fatal("a run kind names the wrong phase")
	}
	if !domain.LadoDBChangeResultApplied.CountsApplied() || !domain.LadoDBChangeResultBaseline.CountsApplied() ||
		domain.LadoDBChangeResultInterrupted.CountsApplied() || domain.LadoDBChangeResultFailed.CountsApplied() || domain.LadoDBChangeResultRunning.CountsApplied() {
		t.Fatal("CountsApplied counts the wrong results")
	}
	retryable := map[domain.LadoDBChangeRunState]bool{}
	for _, state := range domain.LadoDBChangeRunStates() {
		if state.Retryable() {
			retryable[state] = true
		}
	}
	if len(domain.LadoDBChangeRunStates()) != 8 || !reflect.DeepEqual(retryable, map[domain.LadoDBChangeRunState]bool{
		domain.LadoDBChangeRunAwaitingHuman: true, domain.LadoDBChangeRunFailed: true, domain.LadoDBChangeRunMergeFailed: true,
	}) {
		t.Fatalf("retryable states = %v", retryable)
	}
	if got := domain.LadoDBChangeApplySubject("demo-7", "abc", domain.LadoDBChangeAfterDeploy); got != "demo-7@abc:after_deploy" {
		t.Fatalf("apply subject = %q", got)
	}
	if got := domain.LadoDBChangeSettingsSubject("demo", "back"); got != "demo:back" {
		t.Fatalf("settings subject = %q", got)
	}
	if domain.LadoPresenceDBChanges != "project.db_changes" || domain.LadoPresenceDBChangeApply != "db_change.apply" {
		t.Fatal("the presence actions changed; the desktop app signs them")
	}
	pending := error(&domain.LadoDBChangePendingError{PRURL: "https://github.com/acme/demo/pull/8", HeadSHA: "abc",
		Files: []domain.LadoDBChangeFileRef{{Path: "db/1.sql", BlobSHA: "b1", Phase: domain.LadoDBChangePreMerge}}})
	var typed *domain.LadoDBChangePendingError
	if !errors.Is(pending, domain.ErrLadoDBChangePending) || !errors.As(fmt.Errorf("gate: %w", pending), &typed) || len(typed.Files) != 1 {
		t.Fatalf("LadoDBChangePendingError = %v", pending)
	}
}

func TestLadoNoticeDBChange(t *testing.T) {
	for _, tc := range []struct {
		got  string
		want string
	}{
		{domain.LadoNoticeDBChangeFailed(97, "db/002_add.sql"), "LADO 알림: #97 운영 DB SQL 적용 실패 — db/002_add.sql, 병합하지 않았습니다. 앱에서 확인 후 다시 시도"},
		{domain.LadoNoticeDBChangeFailed(97, "db/`x`\n.sql"), "LADO 알림: #97 운영 DB SQL 적용 실패 — db/ x .sql, 병합하지 않았습니다. 앱에서 확인 후 다시 시도"},
		{domain.LadoNoticeDBChangeMergeFailed(97), "LADO 알림: #97 운영 DB SQL 은 적용됐지만 병합 실패 — 앱에서 병합하거나 다시 시도"},
		{domain.LadoNoticeDBChangePreflightBlocked(97, []string{"unresolved_comments", "ci_failing"}), "LADO 알림: #97 운영 DB SQL 적용 보류 — 병합할 수 없습니다(unresolved_comments, ci_failing). SQL 은 적용하지 않았습니다"},
		{domain.LadoNoticeDBChangePreflightBlocked(97, nil), "LADO 알림: #97 운영 DB SQL 적용 보류 — 병합할 수 없습니다(알 수 없는 이유). SQL 은 적용하지 않았습니다"},
		{domain.LadoNoticeDBChangeAfterDeploy(97, 2), "LADO 알림: #97 배포 뒤 적용할 SQL 2개 남음 — 배포가 끝나면 앱에서 적용"},
		{domain.LadoNoticeDBChangeMergedOutside(97, 1), "LADO 알림: #97 LADO 밖에서 병합됨 — 병합 전 SQL 1개가 적용되지 않았습니다. 앱에서 적용"},
		{domain.LadoNoticeDBChangeUnknown(97), "LADO 알림: #97 운영 DB SQL 적용 여부 확인 불가 — 앱에서 확인"},
		{domain.LadoNoticeDBChangeUnknown(0), "LADO 알림: 운영 DB SQL 적용 여부 확인 불가 — 앱에서 확인"},
	} {
		if tc.got != tc.want {
			t.Errorf("notice = %q, want %q", tc.got, tc.want)
		}
	}
	long := domain.LadoNoticeDBChangeFailed(97, strings.Repeat("a", 300))
	if utf8.RuneCountInString(long) != domain.LadoNoticeMaxRunes || !strings.HasSuffix(long, "…") {
		t.Fatalf("a long notice is not cut: %q", long)
	}
}
