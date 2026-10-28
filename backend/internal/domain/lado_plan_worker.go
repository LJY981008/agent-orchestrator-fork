package domain

// LADO fork: plan workers (docs/LJY-PRD/TDD.md §5.1,
// docs/LJY-PRD/adr/0007-plan-workers.md). A plan request starts a read-only
// session of its own, the designer, which drafts the plan; LADO has a second
// read-only session, the critic, review the draft unless the work is small,
// sends the findings back, and pins the final plan the designer submits. The
// project's orchestrator only relays one-line notices.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// LadoPlanRequestKind says who writes a request's plan.
type LadoPlanRequestKind string

// Plan request kinds.
const (
	// LadoPlanRequestOrchestrator is a request LADO sent to the project's
	// orchestrator, as every request was before plan workers. LADO still pins
	// what such a request's orchestrator writes.
	LadoPlanRequestOrchestrator LadoPlanRequestKind = "orchestrator"
	// LadoPlanRequestWorker is a request a plan worker writes.
	LadoPlanRequestWorker LadoPlanRequestKind = "worker"
)

// LadoPlanRole is the role of a plan worker session.
type LadoPlanRole string

// Plan worker roles.
const (
	// LadoPlanRoleDesigner drafts the plan, takes the critique and submits
	// the final plan.
	LadoPlanRoleDesigner LadoPlanRole = "design"
	// LadoPlanRoleCritic reviews the draft adversarially. It sees the draft
	// and its own instructions only.
	LadoPlanRoleCritic LadoPlanRole = "critique"
)

// ladoPlanClientRequestPrefix starts the client request id of every plan
// worker session. It differs from a dispatch attempt's "lado:<entry>/<n>",
// which ParseLadoClientRequestID never reads from it.
const ladoPlanClientRequestPrefix = "lado:plan/"

// LadoPlanWorkerClientRequestID is the client request id of one plan worker
// session: "lado:plan/<request>/<role>/<attempt>". Each attempt has its own,
// because AO keeps a request id that failed unusable.
func LadoPlanWorkerClientRequestID(req LadoPlanRequestID, role LadoPlanRole, attempt int) string {
	return ladoPlanClientRequestPrefix + string(req) + "/" + string(role) + "/" + strconv.Itoa(attempt)
}

// ParseLadoPlanWorkerClientRequestID reads back an id
// LadoPlanWorkerClientRequestID made. It accepts only that exact form.
func ParseLadoPlanWorkerClientRequestID(s string) (LadoPlanRequestID, LadoPlanRole, int, bool) {
	rest, ok := strings.CutPrefix(s, ladoPlanClientRequestPrefix)
	if !ok {
		return "", "", 0, false
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 3 || parts[0] == "" {
		return "", "", 0, false
	}
	role := LadoPlanRole(parts[1])
	if role != LadoPlanRoleDesigner && role != LadoPlanRoleCritic {
		return "", "", 0, false
	}
	attempt, err := strconv.Atoi(parts[2])
	if err != nil || attempt < 1 || strconv.Itoa(attempt) != parts[2] {
		return "", "", 0, false
	}
	return LadoPlanRequestID(parts[0]), role, attempt, true
}

// IsLadoPlanWorkerClientRequestID reports whether s names a plan worker
// session.
func IsLadoPlanWorkerClientRequestID(s string) bool {
	_, _, _, ok := ParseLadoPlanWorkerClientRequestID(s)
	return ok
}

// LadoPlanWorkerHash is the client request hash of a plan worker spawn: the
// hex sha256 of its client request id. The worker gate grants the spawn only
// with it.
func LadoPlanWorkerHash(clientRequestID string) string {
	sum := sha256.Sum256([]byte("lado-plan-worker\n" + clientRequestID))
	return hex.EncodeToString(sum[:])
}

// LadoPlanRequestPlaceholder is the session id a plan worker request holds
// until its designer starts: no AO session id has a colon, so it names none.
func LadoPlanRequestPlaceholder(req LadoPlanRequestID) SessionID {
	return SessionID(ladoPlanClientRequestPrefix + string(req))
}

// LadoPlanWorkerEnv marks a plan worker's launch environment. AO's hook
// command then answers the agent's tool calls with LadoPlanWorkerToolDecision,
// and the Claude Code Chat driver passes the tool lists below.
const LadoPlanWorkerEnv = "AO_LADO_PLAN_WORKER"

// Why a plan worker request failed (docs/LJY-PRD/TDD.md §5.1). The app shows
// each in the developer's language.
const (
	// LadoPlanWorkerSpawnFailed: the designer did not start, also on the
	// second try.
	LadoPlanWorkerSpawnFailed = "plan_worker_spawn_failed"
	// LadoPlanWorkerNoPlan: the designer's session ended, or its turn ended
	// twice, without the plan it was asked for.
	LadoPlanWorkerNoPlan = "plan_worker_no_plan"
	// LadoPlanWorkerTimedOut: a design, revision or the whole request ran
	// past its time.
	LadoPlanWorkerTimedOut = "plan_worker_timed_out"
	// LadoPlanWorkerAgentUnsupported: the project's orchestrator agent is
	// one LADO cannot keep read-only.
	LadoPlanWorkerAgentUnsupported = "plan_worker_agent_unsupported"
	// LadoPlanRequestReplaced: a newer request for the task replaced it.
	LadoPlanRequestReplaced = "replaced"
)

// Why a plan worker request waits for its designer to start.
const (
	// LadoPlanWaitingCapacity: as many plan requests run as the Runs
	// settings allow.
	LadoPlanWaitingCapacity = "plan_worker_capacity"
	// LadoPlanWaitingMemory: memory is tight and the Runs settings hold new
	// work then.
	LadoPlanWaitingMemory = "memory_tight"
)

// The time each step of a plan worker request may take
// (docs/LJY-PRD/TDD.md §5.1). Waiting for a slot does not count.
const (
	LadoPlanDesignTimeout   = 60 * time.Minute
	LadoPlanCritiqueTimeout = 45 * time.Minute
	LadoPlanReviseTimeout   = 30 * time.Minute
	LadoPlanRequestTimeout  = 150 * time.Minute
	// LadoPlanFixWait is how long LADO waits for a fixed plan after it sent a
	// pinned revision's problems back once.
	LadoPlanFixWait = 15 * time.Minute
)

// LadoPlanWorkerPhase is where a plan worker request is. It is derived from
// the request's times and never stored.
type LadoPlanWorkerPhase string

// Plan worker phases.
const (
	LadoPlanPhaseWaiting    LadoPlanWorkerPhase = "waiting"
	LadoPlanPhaseDesigning  LadoPlanWorkerPhase = "designing"
	LadoPlanPhaseCritiquing LadoPlanWorkerPhase = "critiquing"
	LadoPlanPhaseRevising   LadoPlanWorkerPhase = "revising"
	LadoPlanPhaseSubmitted  LadoPlanWorkerPhase = "submitted"
	LadoPlanPhaseFixing     LadoPlanWorkerPhase = "fixing"
	LadoPlanPhaseDone       LadoPlanWorkerPhase = "done"
	LadoPlanPhaseFailed     LadoPlanWorkerPhase = "failed"
	LadoPlanPhaseReplaced   LadoPlanWorkerPhase = "replaced"
)

// IsWorker reports whether a plan worker writes the request's plan.
func (r LadoPlanRequest) IsWorker() bool {
	return r.Kind == LadoPlanRequestWorker
}

// WorkerSession returns the request's designer session, or "" while it has
// none.
func (r LadoPlanRequest) WorkerSession() SessionID {
	if !r.IsWorker() || r.SessionID == LadoPlanRequestPlaceholder(r.ID) {
		return ""
	}
	return r.SessionID
}

// Open reports whether a plan worker request still runs: it neither failed
// nor was cleaned up.
func (r LadoPlanRequest) Open() bool {
	return r.IsWorker() && r.FailedAt.IsZero() && r.CleanedAt.IsZero()
}

// WorkerPhase derives the phase of a plan worker request; "" for a request
// to an orchestrator.
func (r LadoPlanRequest) WorkerPhase() LadoPlanWorkerPhase {
	switch {
	case !r.IsWorker():
		return ""
	case !r.FailedAt.IsZero() && r.FailureCode == LadoPlanRequestReplaced:
		return LadoPlanPhaseReplaced
	case !r.FailedAt.IsZero():
		return LadoPlanPhaseFailed
	case !r.CleanedAt.IsZero():
		return LadoPlanPhaseDone
	case r.WorkerStartedAt.IsZero():
		return LadoPlanPhaseWaiting
	case !r.FixRequestedAt.IsZero():
		return LadoPlanPhaseFixing
	case !r.SubmittedAt.IsZero():
		return LadoPlanPhaseSubmitted
	case r.DraftSubmittedAt.IsZero():
		return LadoPlanPhaseDesigning
	case !r.FeedbackSentAt.IsZero():
		return LadoPlanPhaseRevising
	default:
		return LadoPlanPhaseCritiquing
	}
}

// LadoPlanCritiqueID identifies one review of a draft.
type LadoPlanCritiqueID string

// LadoPlanCritiqueState is how a review of a draft went.
type LadoPlanCritiqueState string

// Critique states.
const (
	LadoCritiqueRunning LadoPlanCritiqueState = "running"
	LadoCritiqueDone    LadoPlanCritiqueState = "done"
	LadoCritiqueFailed  LadoPlanCritiqueState = "failed"
	// LadoCritiqueSkipped records that LADO skipped the review of a small
	// plan, and why.
	LadoCritiqueSkipped LadoPlanCritiqueState = "skipped"
)

// LadoPlanCritiqueAttempts is how many critic sessions a draft gets: one
// retry after a critic that did not start, ended without a result or ran
// past its time.
const LadoPlanCritiqueAttempts = 2

// LadoPlanCritique is one review of a request's draft by a critic session, or
// the record that LADO skipped it.
type LadoPlanCritique struct {
	ID        LadoPlanCritiqueID
	RequestID LadoPlanRequestID
	// Attempt counts the critic sessions from 1; a skipped review is 0.
	Attempt int
	// SessionID is the critic session, "" until it starts.
	SessionID     SessionID
	State         LadoPlanCritiqueState
	SkipReason    string
	FailureReason string
	Findings      []LadoPlanFinding
	StartedAt     time.Time
	FinishedAt    time.Time
}

// LadoPlanFindingSeverity is how grave a critic's finding is.
type LadoPlanFindingSeverity string

// Finding severities. P1 and major findings need an answer in the plan.
const (
	LadoFindingP1    LadoPlanFindingSeverity = "P1"
	LadoFindingMajor LadoPlanFindingSeverity = "major"
	LadoFindingMinor LadoPlanFindingSeverity = "minor"
)

// LadoPlanFinding is one finding of a critic.
type LadoPlanFinding struct {
	ID              string                  `json:"id"`
	Severity        LadoPlanFindingSeverity `json:"severity"`
	Title           string                  `json:"title"`
	FailureScenario string                  `json:"failure_scenario,omitempty"`
	Where           string                  `json:"where,omitempty"`
	Suggestion      string                  `json:"suggestion,omitempty"`
}

// Blocking reports whether the designer must answer the finding in the
// plan's review section.
func (f LadoPlanFinding) Blocking() bool {
	return f.Severity == LadoFindingP1 || f.Severity == LadoFindingMajor
}

// Limits on a critique.
const (
	LadoPlanFindingsMax      = 50
	ladoFindingTitleRunes    = 300
	ladoFindingFieldRunes    = 4000
	LadoPlanCritiqueMaxBytes = 256 << 10
)

var ladoFindingID = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,15}$`)

// ErrLadoPlanFindingsInvalid means a critique's findings are malformed. The
// services report it as LADO_PLAN_CRITIQUE_INVALID.
var ErrLadoPlanFindingsInvalid = errors.New("invalid plan findings")

// ValidateLadoPlanFindings refuses more than LadoPlanFindingsMax findings, an
// id that is not a short word or repeats, an unknown severity, a blank title
// and fields over their length.
func ValidateLadoPlanFindings(findings []LadoPlanFinding) error {
	if len(findings) > LadoPlanFindingsMax {
		return fmt.Errorf("%w: at most %d findings, got %d", ErrLadoPlanFindingsInvalid, LadoPlanFindingsMax, len(findings))
	}
	seen := map[string]bool{}
	for i, f := range findings {
		switch {
		case !ladoFindingID.MatchString(f.ID):
			return fmt.Errorf("%w: finding %d has the id %q; use a short word such as C1", ErrLadoPlanFindingsInvalid, i+1, f.ID)
		case seen[f.ID]:
			return fmt.Errorf("%w: the id %s repeats", ErrLadoPlanFindingsInvalid, f.ID)
		case f.Severity != LadoFindingP1 && f.Severity != LadoFindingMajor && f.Severity != LadoFindingMinor:
			return fmt.Errorf("%w: finding %s has the severity %q; use P1, major or minor", ErrLadoPlanFindingsInvalid, f.ID, f.Severity)
		case strings.TrimSpace(f.Title) == "" || utf8.RuneCountInString(f.Title) > ladoFindingTitleRunes:
			return fmt.Errorf("%w: finding %s needs a title of at most %d characters", ErrLadoPlanFindingsInvalid, f.ID, ladoFindingTitleRunes)
		}
		for _, field := range []string{f.FailureScenario, f.Where, f.Suggestion} {
			if utf8.RuneCountInString(field) > ladoFindingFieldRunes {
				return fmt.Errorf("%w: finding %s has a field over %d characters", ErrLadoPlanFindingsInvalid, f.ID, ladoFindingFieldRunes)
			}
		}
		seen[f.ID] = true
	}
	return nil
}

// Counts returns how many findings of each severity the critique has.
func (c LadoPlanCritique) Counts() (p1, major, minor int) {
	for _, f := range c.Findings {
		switch f.Severity {
		case LadoFindingP1:
			p1++
		case LadoFindingMajor:
			major++
		default:
			minor++
		}
	}
	return p1, major, minor
}

// BlockingFindings returns the P1 and major findings, in order.
func (c LadoPlanCritique) BlockingFindings() []LadoPlanFinding {
	var out []LadoPlanFinding
	for _, f := range c.Findings {
		if f.Blocking() {
			out = append(out, f)
		}
	}
	return out
}

// The changes a plan may declare in change_kinds. Any of them makes LADO
// review the plan whatever its size.
const (
	LadoChangeDBSchema     = "db_schema"
	LadoChangeExternalAPI  = "external_api"
	LadoChangeAuth         = "auth"
	LadoChangeDataDeletion = "data_deletion"
)

// LadoChangeKinds are the values change_kinds takes, in display order.
var LadoChangeKinds = []string{LadoChangeDBSchema, LadoChangeExternalAPI, LadoChangeAuth, LadoChangeDataDeletion}

// LadoPlanReviewSettings decide when LADO skips the review of a draft
// (docs/LJY-PRD/TDD.md §5.1): with Skip on, a plan whose affected_paths has at
// most MaxPaths entries and that neither declares nor, by Patterns, touches
// any of LadoChangeKinds is not reviewed.
type LadoPlanReviewSettings struct {
	Skip     bool
	MaxPaths int
	// Patterns infers change kinds from affected_paths: a path entry that a
	// kind's glob matches touches that kind.
	Patterns map[string][]string
}

// LadoPlanReviewMaxPaths is the highest MaxPaths the settings take.
const LadoPlanReviewMaxPaths = 20

// DefaultLadoPlanReviewPatterns are the globs that infer change kinds until
// the developer sets others: migrations and SQL for the database schema, API
// specifications and routes for external APIs, and authentication code.
// Deleting data shows in no path, so only the plan declares it.
func DefaultLadoPlanReviewPatterns() map[string][]string {
	return map[string][]string{
		LadoChangeDBSchema:     {"**/migrations*/**", "**/*.sql", "**/schema.prisma"},
		LadoChangeExternalAPI:  {"**/openapi*", "**/apispec/**", "**/*.proto", "**/routes/**"},
		LadoChangeAuth:         {"**/auth/**", "**/*auth*", "**/*credential*", "**/*token*"},
		LadoChangeDataDeletion: {},
	}
}

// DefaultLadoPlanReviewSettings: skip on, up to three paths, the default
// patterns.
func DefaultLadoPlanReviewSettings() LadoPlanReviewSettings {
	return LadoPlanReviewSettings{Skip: true, MaxPaths: 3, Patterns: DefaultLadoPlanReviewPatterns()}
}

// ErrLadoPlanReviewSettingsInvalid means review skip settings are out of
// range. The services report it as LADO_DISPATCH_SETTINGS_INVALID.
var ErrLadoPlanReviewSettingsInvalid = errors.New("invalid plan review settings")

// Validate refuses MaxPaths outside 1 to LadoPlanReviewMaxPaths, a pattern
// for an unknown change kind, and a glob a plan's affected_paths could not
// hold.
func (s LadoPlanReviewSettings) Validate() error {
	if s.MaxPaths < 1 || s.MaxPaths > LadoPlanReviewMaxPaths {
		return fmt.Errorf("%w: maxPaths must be 1 to %d, got %d", ErrLadoPlanReviewSettingsInvalid, LadoPlanReviewMaxPaths, s.MaxPaths)
	}
	for kind, globs := range s.Patterns {
		if !slices.Contains(LadoChangeKinds, kind) {
			return fmt.Errorf("%w: unknown change kind %q", ErrLadoPlanReviewSettingsInvalid, kind)
		}
		if len(globs) > LadoProtectedPathsMax {
			return fmt.Errorf("%w: at most %d globs for %s", ErrLadoPlanReviewSettingsInvalid, LadoProtectedPathsMax, kind)
		}
		for _, glob := range globs {
			if glob == "" || strings.TrimSpace(glob) != glob || !validLadoAffectedPath(glob) {
				return fmt.Errorf("%w: %s glob %q", ErrLadoPlanReviewSettingsInvalid, kind, glob)
			}
		}
	}
	return nil
}

// LadoPlanReviewDecision is whether LADO reviews a draft, with the change
// kinds it found and, for a skipped review, why.
type LadoPlanReviewDecision struct {
	Skip bool
	// Reason says why the review is skipped, in English for the designer,
	// who copies it into the plan's review section.
	Reason string
	// Kinds are the change kinds the plan declares or its paths touch.
	Kinds []string
}

// DecideLadoPlanReview applies the settings to a draft. match reports whether
// a glob matches a path; the services pass the matcher of affected paths.
func DecideLadoPlanReview(plan LadoPlan, s LadoPlanReviewSettings, match func(pattern, path string) bool) LadoPlanReviewDecision {
	kinds := slices.Clone(plan.ChangeKinds)
	for _, kind := range LadoChangeKinds {
		if slices.Contains(kinds, kind) {
			continue
		}
		for _, glob := range s.Patterns[kind] {
			if slices.ContainsFunc(plan.AffectedPaths, func(p string) bool { return match(glob, p) }) {
				kinds = append(kinds, kind)
				break
			}
		}
	}
	slices.SortFunc(kinds, func(a, b string) int { return slices.Index(LadoChangeKinds, a) - slices.Index(LadoChangeKinds, b) })
	decision := LadoPlanReviewDecision{Kinds: kinds}
	if !s.Skip || len(plan.AffectedPaths) > s.MaxPaths || len(kinds) > 0 {
		return decision
	}
	decision.Skip = true
	decision.Reason = fmt.Sprintf("the plan names %d affected path(s), within the limit of %d, and changes no database schema, external API, authentication or data deletion",
		len(plan.AffectedPaths), s.MaxPaths)
	return decision
}

// Plan problems of the review section (docs/LJY-PRD/TDD.md §5.1). A plan a
// worker wrote must answer its review in a "## 계획 검수" section.
const (
	// LadoPlanReviewMissing: the plan has no review section.
	LadoPlanReviewMissing = "plan_review_missing"
	// LadoPlanReviewUnaddressed: the section does not say, for each P1 and
	// major finding, whether the plan took it and why not when it did not.
	// Detail lists the finding ids.
	LadoPlanReviewUnaddressed = "plan_review_unaddressed"
)

// LadoPlanReviewHeading is the review section's heading. "Plan review" is
// accepted too.
const LadoPlanReviewHeading = "계획 검수"

// LadoPlanReviewSection returns the body of the plan's review section: the
// lines after a level-two heading named LadoPlanReviewHeading or "Plan
// review", up to the next heading of level one or two.
func LadoPlanReviewSection(body string) (string, bool) {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	for i, line := range lines {
		title, ok := strings.CutPrefix(strings.TrimSpace(line), "## ")
		if !ok {
			continue
		}
		title = strings.TrimSpace(strings.TrimRight(title, "#"))
		if title != LadoPlanReviewHeading && !strings.EqualFold(title, "Plan review") {
			continue
		}
		end := len(lines)
		for j := i + 1; j < len(lines); j++ {
			next := strings.TrimSpace(lines[j])
			if strings.HasPrefix(next, "# ") || strings.HasPrefix(next, "## ") {
				end = j
				break
			}
		}
		return strings.Join(lines[i+1:end], "\n"), true
	}
	return "", false
}

// LadoPlanReviewAnswer is how the review section answers one finding.
type LadoPlanReviewAnswer string

// Answers to a finding.
const (
	LadoAnswerAddressed    LadoPlanReviewAnswer = "addressed"
	LadoAnswerNotAddressed LadoPlanReviewAnswer = "not_addressed"
	// LadoAnswerMissing: no line names the finding with an answer, or a
	// "not addressed" answer gives no reason.
	LadoAnswerMissing LadoPlanReviewAnswer = ""
)

// LadoPlanReviewAnswerOf reads the section's answer to finding id: a line that
// names the id and says 반영 (addressed) or 미반영 (not addressed, with a
// reason after it). "addressed" and "not addressed" are accepted too.
func LadoPlanReviewAnswerOf(section, id string) LadoPlanReviewAnswer {
	token := regexp.MustCompile(`(^|[^A-Za-z0-9_-])` + regexp.QuoteMeta(id) + `([^A-Za-z0-9_-]|$)`)
	answer := LadoAnswerMissing
	for _, line := range strings.Split(section, "\n") {
		if !token.MatchString(line) {
			continue
		}
		switch notAddressed, reasoned := notAddressedAnswer(line); {
		case notAddressed && reasoned:
			return LadoAnswerNotAddressed
		case notAddressed:
			// A "not addressed" without a reason answers nothing.
		case strings.Contains(strings.ToLower(line), "반영") || strings.Contains(strings.ToLower(line), "addressed"):
			answer = LadoAnswerAddressed
		}
	}
	return answer
}

// notAddressedAnswer reports whether line answers "not addressed" and whether
// a reason follows the answer.
func notAddressedAnswer(line string) (bool, bool) {
	lower := strings.ToLower(line)
	for _, word := range []string{"미반영", "not addressed"} {
		if at := strings.Index(lower, word); at >= 0 {
			return true, hasReason(lower[at+len(word):])
		}
	}
	return false, false
}

// hasReason reports whether text after a "not addressed" answer holds a
// reason: at least two letters or digits.
func hasReason(text string) bool {
	n := 0
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			n++
		}
	}
	return n >= 2
}

// CheckLadoPlanReview lists the review section's problems for a plan a worker
// wrote: a missing section, and the P1 and major findings it does not answer.
// It also counts the answers.
func CheckLadoPlanReview(body string, findings []LadoPlanFinding) (problems []LadoPlanProblem, addressed, notAddressed int) {
	section, ok := LadoPlanReviewSection(body)
	if !ok {
		return []LadoPlanProblem{{Code: LadoPlanReviewMissing, Field: "plan_review"}}, 0, 0
	}
	var missing []string
	for _, f := range findings {
		if !f.Blocking() {
			continue
		}
		switch LadoPlanReviewAnswerOf(section, f.ID) {
		case LadoAnswerAddressed:
			addressed++
		case LadoAnswerNotAddressed:
			notAddressed++
		default:
			missing = append(missing, f.ID)
		}
	}
	if len(missing) > 0 {
		problems = append(problems, LadoPlanProblem{Code: LadoPlanReviewUnaddressed, Field: "plan_review", Detail: strings.Join(missing, ", ")})
	}
	return problems, addressed, notAddressed
}

// LadoPlanReview is how a revision's plan was reviewed.
type LadoPlanReview string

// Review outcomes of a revision.
const (
	LadoPlanReviewDone    LadoPlanReview = "done"
	LadoPlanReviewSkipped LadoPlanReview = "skipped"
	// LadoPlanReviewFailed: no critic gave a result, also on the second try.
	LadoPlanReviewFailed LadoPlanReview = "failed"
	// LadoPlanReviewNone: an orchestrator wrote the plan, without a review.
	LadoPlanReviewNone LadoPlanReview = "none"
)

// LadoPlanSessionUsage is what one plan worker session used, read from AO's
// usage records when LADO pinned the revision.
type LadoPlanSessionUsage struct {
	SessionID        SessionID    `json:"sessionId"`
	Role             LadoPlanRole `json:"role"`
	InputTokens      int64        `json:"inputTokens"`
	OutputTokens     int64        `json:"outputTokens"`
	CacheReadTokens  int64        `json:"cacheReadTokens"`
	CacheWriteTokens int64        `json:"cacheWriteTokens"`
	// CostNanos is AO's estimate in nano dollars; nil when AO priced none of
	// the session's events.
	CostNanos *int64 `json:"costNanos,omitempty"`
}

// LadoPlanRevisionMetrics are what making one revision took
// (docs/LJY-PRD/TDD.md §5.1): the time of the design, the critique and the
// revision turn, each session's tokens, and the findings and their answers.
type LadoPlanRevisionMetrics struct {
	RevisionID LadoPlanRevisionID
	RequestID  LadoPlanRequestID
	// Design runs from the designer's start to its draft, Revise from the
	// critique's result to the final plan, Critique over all critic
	// sessions.
	Design   time.Duration
	Critique time.Duration
	Revise   time.Duration
	Review   LadoPlanReview
	// The critique's findings by severity, and how the plan answered the P1
	// and major ones.
	P1, Major, Minor        int
	Addressed, NotAddressed int
	Sessions                []LadoPlanSessionUsage
	// ChangeKinds are the change kinds the plan declares or its paths touch.
	ChangeKinds []string
	UpdatedAt   time.Time
}

// LadoOrchestratorNotice is one line LADO tells the project's orchestrator,
// which relays it to the developer (docs/LJY-PRD/TDD.md §5.1). Key makes each
// event a single notice.
type LadoOrchestratorNotice struct {
	ID         int64
	ProjectID  ProjectID
	Key        string
	Text       string
	OccurredAt time.Time
	CreatedAt  time.Time
	SentAt     time.Time
	// SentTo is the orchestrator session the notice went to.
	SentTo    SessionID
	DroppedAt time.Time
}

// Notice limits.
const (
	// LadoNoticeMaxRunes caps one notice line.
	LadoNoticeMaxRunes = 160
	// LadoNoticeKeep is how long a notice waits for an orchestrator.
	LadoNoticeKeep = 24 * time.Hour
	// LadoNoticeMaxPending is how many notices of a project wait at most;
	// the oldest go first.
	LadoNoticeMaxPending = 20
	// LadoNoticePrefix starts every notice line.
	LadoNoticePrefix = "LADO 알림: "
)

// The notice lines (docs/LJY-PRD/TDD.md §5.1), in one place so their wording
// can change without touching what sends them. They never carry plan text.
const (
	ladoNoticeRequested   = "#%d 계획 요청 접수 — 계획 워커가 씁니다"
	ladoNoticeRevision    = "#%d 계획 리비전 %d — 칸반에서 확인"
	ladoNoticePlanFailed  = "#%d 계획 실패: %s"
	ladoNoticeStarted     = "#%d 실행 시작"
	ladoNoticePROpened    = "#%d PR #%d 열림"
	ladoNoticePROpenedURL = "#%d PR 열림"
	ladoNoticeCompleted   = "#%d 완료"
	ladoNoticeRunFailed   = "#%d 실행 실패: %s"
)

// ladoNoticeReasons are the short reasons a notice gives, by code.
var ladoNoticeReasons = map[string]string{
	LadoPlanWorkerSpawnFailed:      "계획 워커가 뜨지 못함",
	LadoPlanWorkerNoPlan:           "계획 없이 끝남",
	LadoPlanWorkerTimedOut:         "시간 초과",
	LadoPlanWorkerAgentUnsupported: "지원하지 않는 에이전트",
	LadoFailedTimedOut:             "실행 시간 초과",
	LadoFailedEndedWithoutPR:       "PR 없이 끝남",
	LadoFailedAttemptsExhausted:    "워커를 띄우지 못함",
}

func ladoNoticeReason(code string) string {
	if reason, ok := ladoNoticeReasons[code]; ok {
		return reason
	}
	return "알 수 없는 이유"
}

// LadoNoticeRequested is the line for a plan request LADO took.
func LadoNoticeRequested(issue int) string { return ladoNoticeLine(ladoNoticeRequested, issue) }

// LadoNoticeRevision is the line for a pinned revision.
func LadoNoticeRevision(issue, number int) string {
	return ladoNoticeLine(ladoNoticeRevision, issue, number)
}

// LadoNoticePlanFailed is the line for a plan request that failed with code.
func LadoNoticePlanFailed(issue int, code string) string {
	return ladoNoticeLine(ladoNoticePlanFailed, issue, ladoNoticeReason(code))
}

// LadoNoticeStarted is the line for a worker LADO started.
func LadoNoticeStarted(issue int) string { return ladoNoticeLine(ladoNoticeStarted, issue) }

// LadoNoticePROpened is the line for a worker's PR, numbered when its URL
// ends in one.
func LadoNoticePROpened(issue int, prURL string) string {
	if n := ladoTrailingNumber(prURL); n > 0 {
		return ladoNoticeLine(ladoNoticePROpened, issue, n)
	}
	return ladoNoticeLine(ladoNoticePROpenedURL, issue)
}

// LadoNoticeCompleted is the line for a task whose work merged.
func LadoNoticeCompleted(issue int) string { return ladoNoticeLine(ladoNoticeCompleted, issue) }

// LadoNoticeRunFailed is the line for a run that failed with code.
func LadoNoticeRunFailed(issue int, code string) string {
	return ladoNoticeLine(ladoNoticeRunFailed, issue, ladoNoticeReason(code))
}

// ladoNoticeLine formats a notice and cuts it to LadoNoticeMaxRunes.
func ladoNoticeLine(format string, args ...any) string {
	line := LadoNoticePrefix + fmt.Sprintf(format, args...)
	if utf8.RuneCountInString(line) > LadoNoticeMaxRunes {
		line = string([]rune(line)[:LadoNoticeMaxRunes-1]) + "…"
	}
	return line
}

func ladoTrailingNumber(s string) int {
	s = strings.TrimRight(s, "/")
	i := strings.LastIndexAny(s, "/!")
	if i < 0 {
		return 0
	}
	n, err := strconv.Atoi(s[i+1:])
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// LadoRuleSummary is what a plan worker learns of the rules a task's worker
// follows now: the rules that are on, the permission ceiling and the
// protected paths.
type LadoRuleSummary struct {
	Rules          []LadoRuleSummaryItem
	Ceiling        PermissionMode
	ProtectedPaths []string
}

// LadoRuleSummaryItem is one rule that is on, with the text the worker gets.
type LadoRuleSummaryItem struct {
	Name        string
	Instruction string
}

// LadoPlanWorkerAllowedTools are the tools a Claude Code plan worker runs
// without asking (docs/LJY-PRD/TDD.md §5.1): reading and searching, and the
// read-only git and gh commands. Submitting a plan goes through the hook
// decision (LadoPlanWorkerToolDecision), so no rule admits a pipe into it.
var LadoPlanWorkerAllowedTools = []string{
	"Read",
	"Grep",
	"Glob",
	"TodoWrite",
	"Bash(git log:*)",
	"Bash(git show:*)",
	"Bash(git diff:*)",
	"Bash(git status:*)",
	"Bash(git ls-files:*)",
	"Bash(git ls-tree:*)",
	"Bash(gh issue view:*)",
	"Bash(gh pr view:*)",
	"Bash(gh pr diff:*)",
}

// LadoPlanWorkerDisallowedTools are refused outright, as a second line behind
// the hook decision: writing files, committing and pushing, PRs, and starting
// or messaging sessions.
var LadoPlanWorkerDisallowedTools = []string{
	"Edit",
	"Write",
	"NotebookEdit",
	"Bash(git commit:*)",
	"Bash(git push:*)",
	"Bash(gh pr create:*)",
	"Bash(gh pr merge:*)",
	"Bash(ao spawn:*)",
	"Bash(ao send:*)",
}
