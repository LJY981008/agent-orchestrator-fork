package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// LadoTaskID identifies a LADO task.
type LadoTaskID string

// ErrLadoIssueHasTask means the issue already belongs to a task that is not
// cancelled. One issue has at most one live task (docs/LJY-PRD/TDD.md §3.1).
var ErrLadoIssueHasTask = errors.New("the issue already has a LADO task")

// LadoTask is LADO's record of one issue it works on (docs/LJY-PRD/TDD.md
// §3.1). The issue on the project's repository is the work item; the task
// keeps the facts LADO last observed about it, and the tracker stays the
// owner of the issue's text and state.
type LadoTask struct {
	ID        LadoTaskID
	ProjectID ProjectID
	// Repo is the repository whose tracker holds the issue. A workspace
	// project has several; the task records which one, also while LADO is
	// still creating the issue.
	Repo TrackerRepo
	// IssueRef is the canonical issue id. It is empty only while LADO creates
	// the issue, and such a task is never shown.
	IssueRef IssueID
	// Agent and Model are the developer's choices for the task's execution.
	Agent       string
	Model       string
	Issue       LadoObservedIssue
	CreatedAt   time.Time
	UpdatedAt   time.Time
	CancelledAt time.Time
}

// LadoObservedIssue is what LADO last read from a task's issue.
type LadoObservedIssue struct {
	Title string
	State NormalizedIssueState
	URL   string
	// SnapshotSHA256 is the sha256 of the issue's canonical snapshot.
	SnapshotSHA256 string
	ObservedAt     time.Time
}

// LadoTaskState is a task's derived state (docs/LJY-PRD/TDD.md §3.2). It is
// computed at read time and never stored.
type LadoTaskState string

// Task states that exist before plans do. Later build steps add the plan,
// queue and execution states.
const (
	LadoTaskDraft          LadoTaskState = "draft"
	LadoTaskCancelled      LadoTaskState = "cancelled"
	LadoTaskClosedUnmerged LadoTaskState = "closed_unmerged"
)

// State derives the task's state. A task that is neither cancelled nor tied to
// a closed issue is a Draft until plans exist.
func (t LadoTask) State() LadoTaskState {
	switch {
	case !t.CancelledAt.IsZero():
		return LadoTaskCancelled
	case t.Issue.State == IssueDone || t.Issue.State == IssueCancelled:
		return LadoTaskClosedUnmerged
	default:
		return LadoTaskDraft
	}
}

// LadoTaskMarker is the hidden marker LADO appends to the body of an issue it
// creates for a task. After an interrupted request, LADO finds the issue by
// its marker (docs/LJY-PRD/TDD.md §5.1).
func LadoTaskMarker(id LadoTaskID) string {
	return "<!-- lado-task:" + string(id) + " -->"
}

// SplitLadoTaskText splits New Task composer text into an issue title, the
// first non-blank line, and a body, the lines after it.
func SplitLadoTaskText(text string) (title, body string) {
	text = strings.TrimLeft(strings.ReplaceAll(text, "\r\n", "\n"), " \t\n")
	first, rest, _ := strings.Cut(text, "\n")
	lines := strings.Split(rest, "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	return strings.TrimSpace(first), strings.TrimRight(strings.Join(lines, "\n"), " \t\n")
}

// LadoIssueSnapshot is an issue as LADO pins it: its canonical id, title and
// body (docs/LJY-PRD/TDD.md §5.1). Its hash shows whether an issue changed.
type LadoIssueSnapshot struct {
	ID    IssueID `json:"id"`
	Title string  `json:"title"`
	Body  string  `json:"body"`
}

// CanonicalJSON is the snapshot's stored form: the fields in declaration
// order, with HTML characters left as they are. Changing it changes every
// snapshot hash.
func (s LadoIssueSnapshot) CanonicalJSON() []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	// Encoding a struct of strings cannot fail.
	_ = enc.Encode(s)
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
}

// SHA256 returns the hex sha256 of the canonical JSON.
func (s LadoIssueSnapshot) SHA256() string {
	sum := sha256.Sum256(s.CanonicalJSON())
	return hex.EncodeToString(sum[:])
}

// LadoIssueRef returns an issue's canonical id, in the form AO stores in
// sessions.issue_id: "github:owner/repo#N", "gitlab:group/project#N", plus
// "@host" for self-managed GitLab.
func LadoIssueRef(id TrackerID) IssueID {
	native := strings.TrimSpace(id.Native)
	if native == "" {
		return ""
	}
	provider := id.Provider
	if provider == "" {
		provider = TrackerProviderGitHub
	}
	ref := string(provider) + ":" + native
	if host := strings.TrimSpace(id.Host); host != "" {
		ref += "@" + host
	}
	return IssueID(ref)
}

// ParseLadoIssueRef parses a canonical issue id back into a tracker id. It
// accepts only the exact form LadoIssueRef produces.
func ParseLadoIssueRef(ref IssueID) (TrackerID, error) {
	provider, repo, host, number, err := parseLadoRef(string(ref), true)
	if err != nil {
		return TrackerID{}, fmt.Errorf("invalid issue ref %q: %w", string(ref), err)
	}
	return TrackerID{Provider: provider, Native: repo + "#" + number, Host: host}, nil
}

// LadoRepoRef returns a repository's canonical id: the canonical issue id
// form without the issue number, such as "github:owner/repo" or
// "gitlab:group/project@host".
func LadoRepoRef(repo TrackerRepo) string {
	native := strings.TrimSpace(repo.Native)
	if native == "" {
		return ""
	}
	provider := repo.Provider
	if provider == "" {
		provider = TrackerProviderGitHub
	}
	ref := string(provider) + ":" + native
	if host := strings.TrimSpace(repo.Host); host != "" {
		ref += "@" + host
	}
	return ref
}

// ParseLadoRepoRef parses a canonical repository id back. It accepts only
// the exact form LadoRepoRef produces.
func ParseLadoRepoRef(ref string) (TrackerRepo, error) {
	provider, repo, host, _, err := parseLadoRef(ref, false)
	if err != nil {
		return TrackerRepo{}, fmt.Errorf("invalid repository ref %q: %w", ref, err)
	}
	return TrackerRepo{Provider: provider, Native: repo, Host: host}, nil
}

// LadoIssueRepo returns the repository an issue belongs to.
func LadoIssueRepo(id TrackerID) TrackerRepo {
	native := id.Native
	if hash := strings.LastIndexByte(native, '#'); hash >= 0 {
		native = native[:hash]
	}
	return TrackerRepo{Provider: id.Provider, Native: native, Host: id.Host}
}

// parseLadoRef reads "provider:path[#number][@host]". withNumber says
// whether the "#number" part is required (issue refs) or refused
// (repository refs).
func parseLadoRef(raw string, withNumber bool) (provider TrackerProvider, repo, host, number string, err error) {
	name, rest, ok := strings.Cut(raw, ":")
	if !ok || strings.TrimSpace(raw) != raw {
		return "", "", "", "", errors.New("want provider:path")
	}
	provider = TrackerProvider(name)
	if at := strings.LastIndexByte(rest, '@'); at >= 0 {
		rest, host = rest[:at], rest[at+1:]
		if host == "" || provider != TrackerProviderGitLab {
			return "", "", "", "", errors.New("only self-managed GitLab refs name a host")
		}
	}
	repo = rest
	if hash := strings.LastIndexByte(rest, '#'); hash >= 0 {
		if !withNumber {
			return "", "", "", "", errors.New("a repository ref has no #number")
		}
		repo, number = rest[:hash], rest[hash+1:]
		if n, convErr := strconv.Atoi(number); convErr != nil || n <= 0 || strconv.Itoa(n) != number {
			return "", "", "", "", errors.New("bad issue number")
		}
	} else if withNumber {
		return "", "", "", "", errors.New("missing #number")
	}
	segments := strings.Split(repo, "/")
	switch provider {
	case TrackerProviderGitHub:
		if len(segments) != 2 {
			return "", "", "", "", errors.New("want owner/repo")
		}
	case TrackerProviderGitLab:
		if len(segments) < 2 {
			return "", "", "", "", errors.New("want group/project")
		}
	default:
		return "", "", "", "", errors.New("unknown provider")
	}
	for _, segment := range segments {
		if segment == "" || strings.ContainsAny(segment, "#@ \t\r\n") {
			return "", "", "", "", errors.New("bad path segment")
		}
	}
	return provider, repo, host, number, nil
}

// LadoIssueNumber returns the issue number of a tracker id, or 0 when the id
// has none.
func LadoIssueNumber(id TrackerID) int {
	hash := strings.LastIndexByte(id.Native, '#')
	if hash < 0 {
		return 0
	}
	n, err := strconv.Atoi(id.Native[hash+1:])
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// LadoTrackerRepoFromOrigin returns the tracker repository a project's origin
// names. LADO supports github.com, gitlab.com and self-managed GitLab; GitHub
// Enterprise is out because AO's GitHub API base is fixed to api.github.com.
// The host drops any port, because an SSH port says nothing about the API.
func LadoTrackerRepoFromOrigin(origin string) (TrackerRepo, error) {
	identity, err := ParseRepositoryIdentity(origin)
	if err != nil {
		return TrackerRepo{}, err
	}
	host := identity.Host
	if hostname, _, err := net.SplitHostPort(host); err == nil {
		host = hostname
	}
	host = strings.ToLower(host)
	native := identity.Namespace + "/" + identity.Name
	if RepositoryProvider(host) == "github" {
		if host != "github.com" && host != "www.github.com" {
			return TrackerRepo{}, fmt.Errorf("repository host %s: LADO supports GitHub on github.com only", host)
		}
		return TrackerRepo{Provider: TrackerProviderGitHub, Native: native}, nil
	}
	if host == "gitlab.com" || host == "www.gitlab.com" {
		host = ""
	}
	return TrackerRepo{Provider: TrackerProviderGitLab, Native: native, Host: host}, nil
}

// LadoSameRepo reports whether two tracker repositories are the same one.
// Repository paths compare without case, as GitHub and GitLab treat them.
func LadoSameRepo(a, b TrackerRepo) bool {
	return a.Provider == b.Provider && strings.EqualFold(a.Host, b.Host) && strings.EqualFold(a.Native, b.Native)
}
