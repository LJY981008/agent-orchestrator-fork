package domain

// LADO fork: parallel parts (docs/LJY-PRD/TDD.md §5.4). A plan may split its
// steps into parts; LADO runs each part in a fresh worker session on its own
// branch, merges the parts into one integration branch as they finish, and
// starts one integration worker that reviews the merged result, does the
// steps no part took and opens the task's one pull request.

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// LadoPlanPart is one entry of a plan's parts list.
type LadoPlanPart struct {
	ID    string `yaml:"id"`
	Title string `yaml:"title"`
	// Steps are 1-based numbers into the plan's steps.
	Steps []int `yaml:"steps"`
	// AffectedPaths are the part's own globs, inside the plan's.
	AffectedPaths []string `yaml:"affected_paths"`
	// DependsOn names the parts that must be merged before this one starts.
	DependsOn []string `yaml:"depends_on"`
}

// The limits of a parts list: a plan that names parts has 2 to 8 of them.
const (
	LadoPlanMinParts = 2
	LadoPlanMaxParts = 8
)

// LadoPlanLargeSteps is how many steps make a plan large enough that the plan
// view warns when it names no parts.
const LadoPlanLargeSteps = 6

// Plan problem codes of a parts list. Field is "parts"; Detail names the part
// and the offending value.
const (
	LadoPlanPartCount             = "part_count"
	LadoPlanPartInvalidID         = "part_invalid_id"
	LadoPlanPartDuplicateID       = "part_duplicate_id"
	LadoPlanPartUnknownDependency = "part_unknown_dependency"
	LadoPlanPartCycle             = "part_cycle"
	LadoPlanPartStepOutOfRange    = "part_step_out_of_range"
	LadoPlanPartStepShared        = "part_step_shared"
	LadoPlanPartPathOutside       = "part_path_outside"
	LadoPlanPartMissing           = "part_missing_field"
)

// LadoPlanWarningUnsplit warns of a large plan that names no parts: one
// worker runs it from start to end. A warning never blocks the approval.
const LadoPlanWarningUnsplit = "unsplit_large_plan"

// ladoPartID is the form of a part id: lower case, it starts a branch name
// segment.
var ladoPartID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)

// ladoPartReservedID is the id of the integration worker's branch, and
// ladoMergePrefix starts the merge workers' branches; no part takes them.
const (
	ladoPartReservedID = "integration"
	ladoMergePrefix    = "merge-"
)

// ValidLadoPartID reports whether id can name a part.
func ValidLadoPartID(id string) bool {
	return ladoPartID.MatchString(id) && id != ladoPartReservedID && !strings.HasPrefix(id, ladoMergePrefix)
}

// Warnings lists what the plan view warns about, without blocking the
// approval: a large plan without parts.
func (p LadoPlan) Warnings() []string {
	if len(p.Parts) == 0 && len(p.Steps) >= LadoPlanLargeSteps {
		return []string{LadoPlanWarningUnsplit}
	}
	return nil
}

// IntegrationSteps returns the 1-based numbers of the steps no part takes:
// the integration worker does them once every part is merged.
func (p LadoPlan) IntegrationSteps() []int {
	if len(p.Parts) == 0 {
		return nil
	}
	taken := map[int]bool{}
	for _, part := range p.Parts {
		for _, n := range part.Steps {
			taken[n] = true
		}
	}
	var rest []int
	for n := 1; n <= len(p.Steps); n++ {
		if !taken[n] {
			rest = append(rest, n)
		}
	}
	return rest
}

// parts reads the optional parts list: missing or null is none.
func (r *planReader) parts() []LadoPlanPart {
	node, ok := r.fields["parts"]
	if !ok || node.Tag == "!!null" {
		return nil
	}
	var raw []yaml.Node
	if node.Kind != yaml.SequenceNode || node.Decode(&raw) != nil {
		r.add(LadoPlanInvalidField, "parts", "")
		return nil
	}
	parts := make([]LadoPlanPart, 0, len(raw))
	for i, item := range raw {
		var part LadoPlanPart
		if item.Kind != yaml.MappingNode || item.Decode(&part) != nil {
			r.add(LadoPlanInvalidField, "parts", strconv.Itoa(i+1))
			return nil
		}
		part.ID = strings.TrimSpace(part.ID)
		part.Title = strings.TrimSpace(part.Title)
		part.AffectedPaths = trimAll(part.AffectedPaths)
		part.DependsOn = trimAll(part.DependsOn)
		parts = append(parts, part)
	}
	return parts
}

func trimAll(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, strings.TrimSpace(v))
	}
	return out
}

// checkParts lists the problems of a plan's parts: their count, ids,
// dependencies, steps and paths (docs/LJY-PRD/TDD.md §5.4).
func (r *planReader) checkParts(plan LadoPlan) {
	if len(plan.Parts) == 0 {
		if _, ok := r.fields["parts"]; ok && r.fields["parts"].Tag != "!!null" && r.fields["parts"].Kind == yaml.SequenceNode {
			r.add(LadoPlanPartCount, "parts", "0")
		}
		return
	}
	if n := len(plan.Parts); n < LadoPlanMinParts || n > LadoPlanMaxParts {
		r.add(LadoPlanPartCount, "parts", strconv.Itoa(n))
	}
	ids := map[string]bool{}
	for _, part := range plan.Parts {
		switch {
		case !ValidLadoPartID(part.ID):
			r.add(LadoPlanPartInvalidID, "parts", part.ID)
		case ids[part.ID]:
			r.add(LadoPlanPartDuplicateID, "parts", part.ID)
		}
		ids[part.ID] = true
		if part.Title == "" {
			r.add(LadoPlanPartMissing, "parts", part.ID+": title")
		}
		if len(part.Steps) == 0 {
			r.add(LadoPlanPartMissing, "parts", part.ID+": steps")
		}
		if len(part.AffectedPaths) == 0 {
			r.add(LadoPlanPartMissing, "parts", part.ID+": affected_paths")
		}
	}
	owner := map[int]string{}
	for _, part := range plan.Parts {
		for _, dep := range part.DependsOn {
			if !ids[dep] || dep == part.ID {
				r.add(LadoPlanPartUnknownDependency, "parts", part.ID+": "+dep)
			}
		}
		for _, n := range part.Steps {
			if n < 1 || n > len(plan.Steps) {
				r.add(LadoPlanPartStepOutOfRange, "parts", fmt.Sprintf("%s: %d", part.ID, n))
				continue
			}
			if other, taken := owner[n]; taken {
				r.add(LadoPlanPartStepShared, "parts", fmt.Sprintf("%s, %s: %d", other, part.ID, n))
				continue
			}
			owner[n] = part.ID
		}
		for _, glob := range part.AffectedPaths {
			if !validLadoAffectedPath(glob) || !ladoPathInside(glob, plan.AffectedPaths) {
				r.add(LadoPlanPartPathOutside, "parts", part.ID+": "+glob)
			}
		}
	}
	if cycle := ladoPartCycle(plan.Parts); cycle != "" {
		r.add(LadoPlanPartCycle, "parts", cycle)
	}
}

// ladoPartCycle returns the id of a part on a dependency cycle, or "". Unknown
// dependencies are left out; checkParts reports them.
func ladoPartCycle(parts []LadoPlanPart) string {
	deps := map[string][]string{}
	for _, part := range parts {
		deps[part.ID] = part.DependsOn
	}
	const (
		visiting = 1
		done     = 2
	)
	mark := map[string]int{}
	var visit func(id string) bool
	visit = func(id string) bool {
		switch mark[id] {
		case visiting:
			return true
		case done:
			return false
		}
		mark[id] = visiting
		for _, dep := range deps[id] {
			if _, known := deps[dep]; known && visit(dep) {
				return true
			}
		}
		mark[id] = done
		return false
	}
	for _, part := range parts {
		if visit(part.ID) {
			return part.ID
		}
	}
	return ""
}

// LadoPartPhases groups parts into phases for display: a part without
// dependencies is in phase 1, and one that depends on others is one phase
// after the latest of them. The phases say nothing about when a part starts;
// it starts once the parts it depends on are merged. Parts on a cycle, which
// a valid plan has none of, get phase 0. Each phase keeps the plan's order.
func LadoPartPhases(parts []LadoPlanPart) map[string]int {
	byID := map[string]LadoPlanPart{}
	for _, part := range parts {
		byID[part.ID] = part
	}
	phase := map[string]int{}
	var depth func(id string, seen map[string]bool) int
	depth = func(id string, seen map[string]bool) int {
		if p, ok := phase[id]; ok {
			return p
		}
		if seen[id] {
			return 0
		}
		seen[id] = true
		p := 1
		for _, dep := range byID[id].DependsOn {
			if _, known := byID[dep]; !known {
				continue
			}
			d := depth(dep, seen)
			if d == 0 {
				return 0
			}
			p = max(p, d+1)
		}
		phase[id] = p
		return p
	}
	for _, part := range parts {
		depth(part.ID, map[string]bool{})
		if _, ok := phase[part.ID]; !ok {
			phase[part.ID] = 0
		}
	}
	return phase
}

// ladoGlobPrefix splits a glob into the path segments before its first
// segment with a wildcard, lower case.
func ladoGlobPrefix(glob string) []string {
	var prefix []string
	for _, segment := range strings.Split(strings.ToLower(glob), "/") {
		if strings.ContainsAny(segment, "*?[") {
			break
		}
		prefix = append(prefix, segment)
	}
	return prefix
}

// segmentPrefix reports whether a is b or a leading part of it.
func segmentPrefix(a, b []string) bool {
	return len(a) <= len(b) && slices.Equal(a, b[:len(a)])
}

// ladoPathInside reports whether glob lies inside one of the plan's globs:
// its fixed prefix starts with that glob's fixed prefix.
func ladoPathInside(glob string, plan []string) bool {
	prefix := ladoGlobPrefix(glob)
	for _, outer := range plan {
		if segmentPrefix(ladoGlobPrefix(outer), prefix) {
			return true
		}
	}
	return false
}

// LadoDefaultOverlapIgnore are the globs whose overlap does not hold work
// back until a project sets its own: files that many changes append to or
// regenerate, whose conflicts resolve easily.
var LadoDefaultOverlapIgnore = []string{
	"frontend/src/renderer/i18n/*.json",
	"DESIGN.md",
	"LADO.md",
	"LADO.ko.md",
	"docs/**",
	"frontend/src/api/schema.ts",
	"backend/internal/httpd/apispec/openapi.yaml",
	"backend/internal/storage/sqlite/gen/**",
	"frontend/src/main/tray-catalog.generated.ts",
}

// LadoGlobsOverlap reports whether two globs may name the same file: the
// fixed prefix of one, by path segments and without case, starts the other's.
// The rule is generous on purpose: work that might touch the same file waits.
func LadoGlobsOverlap(a, b string) bool {
	pa, pb := ladoGlobPrefix(a), ladoGlobPrefix(b)
	return segmentPrefix(pa, pb) || segmentPrefix(pb, pa)
}

// LadoPathsOverlap returns the first pair of globs from a and b that may name
// the same file, leaving out a pair whose narrower glob only names files an
// ignore glob covers. ok is false when no pair overlaps.
func LadoPathsOverlap(a, b, ignore []string) (string, string, bool) {
	for _, x := range a {
		for _, y := range b {
			if !LadoGlobsOverlap(x, y) {
				continue
			}
			narrow := y
			if len(ladoGlobPrefix(x)) > len(ladoGlobPrefix(y)) {
				narrow = x
			}
			if ladoGlobCovered(narrow, ignore) || (ladoGlobCovered(x, ignore) && ladoGlobCovered(y, ignore)) {
				continue
			}
			return x, y, true
		}
	}
	return "", "", false
}

// ladoGlobCovered reports whether every file glob names is one an ignore glob
// names: a "dir/**" glob covers what lies under dir, and another glob covers
// what it matches, a glob it matches as text among them.
func ladoGlobCovered(glob string, ignore []string) bool {
	lower := strings.ToLower(glob)
	for _, pattern := range ignore {
		p := strings.ToLower(strings.TrimSpace(pattern))
		if p == "" {
			continue
		}
		if base, ok := strings.CutSuffix(p, "/**"); ok && !strings.ContainsAny(base, "*?[") {
			if strings.HasPrefix(lower, base+"/") {
				return true
			}
			continue
		}
		if p == lower {
			return true
		}
		if ok, err := path.Match(p, lower); err == nil && ok {
			return true
		}
	}
	return false
}

// LadoPartState is the state of one part of a task's parts run.
type LadoPartState string

// Part states. A part waits until it can start, runs in its worker, is done
// once its worker reports done, merging while LADO merges it, merged once its
// commits are on the integration branch, in conflict while a merge worker
// resolves its merge, and failed when its attempts ran out.
const (
	LadoPartWaiting  LadoPartState = "waiting"
	LadoPartRunning  LadoPartState = "running"
	LadoPartDone     LadoPartState = "done"
	LadoPartMerging  LadoPartState = "merging"
	LadoPartMerged   LadoPartState = "merged"
	LadoPartConflict LadoPartState = "conflict"
	LadoPartFailed   LadoPartState = "failed"
)

// Active reports whether the part holds its paths: from its start until it is
// merged or failed.
func (s LadoPartState) Active() bool {
	switch s {
	case LadoPartRunning, LadoPartDone, LadoPartMerging, LadoPartConflict:
		return true
	}
	return false
}

// Roles of an execution. A task without parts runs one execution without a
// role; a task with parts runs one execution per part attempt, one per merge
// worker, and one integration execution, which opens the task's pull request.
const (
	LadoRoleSingle      = ""
	LadoRolePart        = "part"
	LadoRoleMerge       = "merge"
	LadoRoleIntegration = "integration"
)

// LadoMaxPartAttempts is how many workers a part, or a part's merge, may
// start before its task fails.
const LadoMaxPartAttempts = 2

// Waiting reasons of parts and of entries, beside the queue's own.
const (
	// LadoWaitingSlotWorkers means as many LADO workers run as the dispatch
	// settings allow.
	LadoWaitingSlotWorkers = "slot_workers"
	// LadoWaitingDiskLow means the free disk space is under the dispatch
	// settings' threshold.
	LadoWaitingDiskLow = "disk_low"
	// LadoWaitingPathsOverlap means a live run of another task of the project
	// may touch the same files; the entry's detail names that task's issue.
	LadoWaitingPathsOverlap = "paths_overlap"
	// LadoWaitingPartDependency means a part waits for the parts it depends
	// on to be merged.
	LadoWaitingPartDependency = "part_dependency"
	// LadoWaitingPartOverlap means a running part of the same task may touch
	// the same files; the part's detail names it.
	LadoWaitingPartOverlap = "part_overlap"
)

// LadoTaskPart is one part of a task's parts run as LADO records it: the
// plan's part and how far it got.
type LadoTaskPart struct {
	QueueEntryID  LadoQueueEntryID
	TaskID        LadoTaskID
	PartID        string
	Title         string
	Position      int
	Phase         int
	Steps         []int
	AffectedPaths []string
	DependsOn     []string
	State         LadoPartState
	// Attempts counts the part's workers so far, MergeAttempts its merge
	// workers.
	Attempts      int
	MergeAttempts int
	WaitingReason string
	WaitingDetail string
	// SessionID and Branch are those of the part's newest worker, StartSHA
	// the integration head its branch started from.
	SessionID SessionID
	Branch    string
	StartSHA  string
	// MergeFromSHA and MergeTipSHA are the merge LADO records before it
	// merges: the integration head and the part's tip. MergedSHA is the
	// integration head once the part is merged.
	MergeFromSHA   string
	MergeTipSHA    string
	MergedSHA      string
	MergeSessionID SessionID
	MergeBranch    string
	// Summary is the part worker's done report.
	Summary   string
	Error     string
	UpdatedAt time.Time
}

// LadoPartRun is a queue entry's parts run: the integration branch the parts
// merge into and where it stands, with the base the dispatch resolved.
type LadoPartRun struct {
	QueueEntryID LadoQueueEntryID
	TaskID       LadoTaskID
	ProjectID    ProjectID
	// RepoDir is the repository the branches live in.
	RepoDir string
	// Branch is the integration branch; BaseSHA the commit it started from,
	// HeadSHA where LADO last moved it.
	Branch  string
	BaseSHA string
	HeadSHA string
	// The base the dispatch resolved, which the integration execution keeps.
	PlanRevisionID       LadoPlanRevisionID
	PlanBaseSHA          string
	BaseCommitsAhead     *int
	BaseBranch           string
	BaseRemote           string
	DispatchLocalBaseSHA string
	StartedAt            time.Time
	// IntegrationAttempts counts the integration worker's starts so far, and
	// IntegrationRetryAt is when a start that failed may be tried again.
	IntegrationAttempts int
	IntegrationRetryAt  time.Time
}

// LadoTaskPartsSummary is how far a task's newest parts run got: merged parts
// of all, for the board card.
type LadoTaskPartsSummary struct {
	Total  int
	Merged int
}

// LadoTaskShortID is the part of a task id branch names use: the first eight
// characters after "lado-task-".
func LadoTaskShortID(id LadoTaskID) string {
	short := strings.TrimPrefix(string(id), "lado-task-")
	if len(short) > 8 {
		short = short[:8]
	}
	return short
}

// LadoEntryShortID is the part of a queue entry id branch names use: the
// first eight characters after "lado-queue-". Each approval queues its own
// entry, so a task approved again runs its parts on branches of their own.
func LadoEntryShortID(id LadoQueueEntryID) string {
	short := strings.TrimPrefix(string(id), "lado-queue-")
	if len(short) > 8 {
		short = short[:8]
	}
	return short
}

// ladoRunBranch is "lado/<task>/<entry>/<name>": the branches of one parts
// run, apart from every other run's, the same task's earlier runs included.
func ladoRunBranch(task LadoTaskID, entry LadoQueueEntryID, name string) string {
	return "lado/" + LadoTaskShortID(task) + "/" + LadoEntryShortID(entry) + "/" + name
}

// LadoIntegrationBranch is the branch a parts run's parts merge into.
func LadoIntegrationBranch(task LadoTaskID, entry LadoQueueEntryID) string {
	return ladoRunBranch(task, entry, ladoPartReservedID)
}

// LadoPartBranch is the branch of a part's attempt: the first attempt has the
// part's id, a later one adds ".<attempt>". A part id has no dot, so no
// attempt's branch is another part's.
func LadoPartBranch(task LadoTaskID, entry LadoQueueEntryID, part string, attempt int) string {
	name := part
	if attempt > 1 {
		name += "." + strconv.Itoa(attempt)
	}
	return ladoRunBranch(task, entry, name)
}

// LadoMergeBranch is the branch of a merge worker's attempt for a part.
func LadoMergeBranch(task LadoTaskID, entry LadoQueueEntryID, part string, attempt int) string {
	return LadoPartBranch(task, entry, ladoMergePrefix+part, attempt)
}

// LadoRunRequest is what a LADO execution's client request id names: the
// queue entry, the role, the part for part and merge workers, and the
// attempt.
type LadoRunRequest struct {
	Entry   LadoQueueEntryID
	Role    string
	Part    string
	Attempt int
}

// LadoRunClientRequestID is the client request id of a worker of a parts
// run: "lado:<entry>/<part>/<attempt>" for a part,
// "lado:<entry>/merge-<part>/<attempt>" for its merge worker and
// "lado:<entry>/integration/<attempt>" for the integration worker. Each
// attempt is a new id, so each worker is a new session.
func LadoRunClientRequestID(r LadoRunRequest) string {
	middle := r.Part
	switch r.Role {
	case LadoRoleSingle:
		return LadoClientRequestID(r.Entry, r.Attempt)
	case LadoRoleMerge:
		middle = ladoMergePrefix + r.Part
	case LadoRoleIntegration:
		middle = ladoPartReservedID
	}
	return ladoClientRequestPrefix + string(r.Entry) + "/" + middle + "/" + strconv.Itoa(r.Attempt)
}

// ParseLadoRunClientRequestID reads back an id of an execution, a single
// worker's (LadoClientRequestID) or a parts run worker's
// (LadoRunClientRequestID). A plan worker's id is none.
func ParseLadoRunClientRequestID(s string) (LadoRunRequest, bool) {
	if entry, attempt, ok := ParseLadoClientRequestID(s); ok {
		return LadoRunRequest{Entry: entry, Attempt: attempt}, true
	}
	if IsLadoPlanWorkerClientRequestID(s) {
		return LadoRunRequest{}, false
	}
	rest, ok := strings.CutPrefix(s, ladoClientRequestPrefix)
	if !ok {
		return LadoRunRequest{}, false
	}
	fields := strings.Split(rest, "/")
	if len(fields) != 3 || fields[0] == "" {
		return LadoRunRequest{}, false
	}
	attempt, err := strconv.Atoi(fields[2])
	if err != nil || attempt < 1 || strconv.Itoa(attempt) != fields[2] {
		return LadoRunRequest{}, false
	}
	r := LadoRunRequest{Entry: LadoQueueEntryID(fields[0]), Attempt: attempt}
	switch middle := fields[1]; {
	case middle == ladoPartReservedID:
		r.Role = LadoRoleIntegration
	case strings.HasPrefix(middle, ladoMergePrefix) && ValidLadoPartID(strings.TrimPrefix(middle, ladoMergePrefix)):
		r.Role, r.Part = LadoRoleMerge, strings.TrimPrefix(middle, ladoMergePrefix)
	case ValidLadoPartID(middle):
		r.Role, r.Part = LadoRolePart, middle
	default:
		return LadoRunRequest{}, false
	}
	return r, true
}

// LadoLiveRunTask is a task that takes a task slot: one with a live
// execution, an active lease or a running parts run, with the plan revision
// it runs.
type LadoLiveRunTask struct {
	TaskID         LadoTaskID
	ProjectID      ProjectID
	IssueRef       IssueID
	PlanRevisionID LadoPlanRevisionID
}

// LadoRunLoad is what runs at once: the tasks that take task slots and how
// many LADO workers take worker slots.
type LadoRunLoad struct {
	Tasks   []LadoLiveRunTask
	Workers int
}

// TasksByProject counts the running tasks of each project.
func (l LadoRunLoad) TasksByProject() map[ProjectID]int {
	seen := map[LadoTaskID]bool{}
	counts := map[ProjectID]int{}
	for _, task := range l.Tasks {
		if !seen[task.TaskID] {
			seen[task.TaskID] = true
			counts[task.ProjectID]++
		}
	}
	return counts
}

// TaskCount counts the running tasks.
func (l LadoRunLoad) TaskCount() int {
	seen := map[LadoTaskID]bool{}
	for _, task := range l.Tasks {
		seen[task.TaskID] = true
	}
	return len(seen)
}

// Runs reports whether the task takes a task slot.
func (l LadoRunLoad) Runs(id LadoTaskID) bool {
	for _, task := range l.Tasks {
		if task.TaskID == id {
			return true
		}
	}
	return false
}

// LadoActivePartRun is a parts run in progress with its queue entry.
type LadoActivePartRun struct {
	Run   LadoPartRun
	Entry LadoQueueEntry
}
