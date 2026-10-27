package ladoverify

// LADO fork: the base branch's remote protection (docs/LJY-PRD/TDD.md §7.1;
// plan decisions d and i). The rules that apply to the base branch name the
// checks it requires, and say whether the remote sends every change through
// LADO's integration approval: a pull request, the lado/integration check,
// and no force push or deletion. Reads go through the repository client with
// LADO's stored token and are cached per repository and branch, since the
// loop asks every pass.

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// RepoClients hands out the client for a repository's host, with the token
// LADO uses there.
type RepoClients interface {
	ForRepo(ctx context.Context, repo domain.TrackerRepo) (ports.LadoRepoClient, error)
}

// ProtectionRead is one read of a branch's rules. When the read failed, Err
// says why and Rules are those of the last read that worked, if any.
type ProtectionRead struct {
	Provider domain.TrackerProvider
	Repo     domain.TrackerRepo
	Branch   string
	Rules    ports.LadoBranchRules
	Err      error
	// ReadAt is when LADO asked the host.
	ReadAt time.Time
}

// MissingIntegrationCheck is the missing part when no required_status_checks
// rule names lado/integration.
const MissingIntegrationCheck = ports.LadoBranchRuleRequiredStatusChecks + ":" + domain.LadoIntegrationStatusContext

// Protection reads branch rules through a cache keyed by repository and
// branch.
type Protection struct {
	clients RepoClients
	now     func() time.Time
	ttl     time.Duration

	mu      sync.Mutex
	entries map[protectionKey]*protectionEntry
}

type protectionKey struct {
	repo, branch string
}

// protectionEntry is one branch's reads. mu is held through a read, so a
// branch is read once at a time.
type protectionEntry struct {
	mu     sync.Mutex
	cached bool
	last   ProtectionRead
	// good are the rules of the last read that worked.
	good ports.LadoBranchRules
}

// NewProtection returns a cache that keeps each read for ttl.
func NewProtection(clients RepoClients, now func() time.Time, ttl time.Duration) *Protection {
	if now == nil {
		now = time.Now
	}
	return &Protection{clients: clients, now: now, ttl: ttl, entries: map[protectionKey]*protectionEntry{}}
}

// Read returns the rules of branch on repo: the last read while it is younger
// than the ttl, else a new one; force always reads again. A caller that finds
// a read of the branch under way waits for it, then takes it from the cache
// unless forced. A failed read keeps the last good rules next to its error
// and is cached like any other; a read its caller cancelled is not kept.
func (p *Protection) Read(ctx context.Context, repo domain.TrackerRepo, branch string, force bool) ProtectionRead {
	e := p.entry(repo, branch)
	e.mu.Lock()
	defer e.mu.Unlock()
	if !force && e.cached && p.now().Sub(e.last.ReadAt) < p.ttl {
		return e.last
	}
	read := ProtectionRead{Provider: repo.Provider, Repo: repo, Branch: branch, ReadAt: p.now()}
	rules, err := p.fetch(ctx, repo, branch)
	if err != nil {
		read.Rules, read.Err = e.good, err
		if ctx.Err() != nil {
			// The caller gave up; the answer says nothing about the host.
			return read
		}
	} else {
		read.Rules, e.good = rules, rules
	}
	e.cached, e.last = true, read
	return read
}

func (p *Protection) entry(repo domain.TrackerRepo, branch string) *protectionEntry {
	key := protectionKey{repo: domain.LadoRepoRef(repo), branch: branch}
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.entries[key]
	if !ok {
		e = &protectionEntry{}
		p.entries[key] = e
	}
	return e
}

func (p *Protection) fetch(ctx context.Context, repo domain.TrackerRepo, branch string) (ports.LadoBranchRules, error) {
	client, err := p.clients.ForRepo(ctx, repo)
	if err != nil {
		return ports.LadoBranchRules{}, err
	}
	return client.BranchRules(ctx, repo, branch)
}

// RequiredContexts returns the checks the rules require, in rule order, each
// once, compared without case as GitHub compares contexts. lado/integration
// is left out: LADO posts it after the approval. named reports whether any
// check is left.
func RequiredContexts(rules ports.LadoBranchRules) (contexts []string, named bool) {
	seen := map[string]bool{}
	for _, rule := range rules.Rules {
		if rule.Type != ports.LadoBranchRuleRequiredStatusChecks {
			continue
		}
		for _, check := range rule.RequiredChecks {
			key := strings.ToLower(check.Context)
			if key == "" || strings.EqualFold(check.Context, domain.LadoIntegrationStatusContext) || seen[key] {
				continue
			}
			seen[key] = true
			contexts = append(contexts, check.Context)
		}
	}
	return contexts, len(contexts) > 0
}

// MissingProtection lists what the rules lack for LADO to count the branch
// as protected, in this order: pull_request, MissingIntegrationCheck,
// non_fast_forward and deletion.
func MissingProtection(rules ports.LadoBranchRules) []string {
	has := map[string]bool{}
	for _, rule := range rules.Rules {
		has[rule.Type] = true
		if rule.Type != ports.LadoBranchRuleRequiredStatusChecks {
			continue
		}
		for _, check := range rule.RequiredChecks {
			if strings.EqualFold(check.Context, domain.LadoIntegrationStatusContext) {
				has[MissingIntegrationCheck] = true
			}
		}
	}
	var missing []string
	for _, part := range []string{ports.LadoBranchRulePullRequest, MissingIntegrationCheck, ports.LadoBranchRuleNonFastForward, ports.LadoBranchRuleDeletion} {
		if !has[part] {
			missing = append(missing, part)
		}
	}
	return missing
}

// EvaluateProtection is the protection item for a read. LADO does not read
// GitLab's protection, so a GitLab project is unprotected_gitlab; a failed
// read is protection_unknown with its error on one line; a branch missing a
// part is unprotected; else the item passes. The details name the required
// checks, from the last good read when this one failed.
func EvaluateProtection(read ProtectionRead) (domain.LadoVerifyItem, error) {
	required, _ := RequiredContexts(read.Rules)
	details := domain.LadoProtectionDetails{
		Provider:       string(read.Provider),
		Repo:           read.Repo.Native,
		Branch:         read.Branch,
		RequiredChecks: required,
	}
	outcome, code := domain.LadoVerifyPass, ""
	switch {
	case read.Provider == domain.TrackerProviderGitLab:
		outcome, code = domain.LadoVerifyConfirm, domain.LadoCodeUnprotectedGitLab
	case read.Err != nil:
		details.Error = redactedErrorLine(read.Err)
		outcome, code = domain.LadoVerifyConfirm, domain.LadoCodeProtectionUnknown
	default:
		if details.Missing = MissingProtection(read.Rules); len(details.Missing) > 0 {
			outcome, code = domain.LadoVerifyConfirm, domain.LadoCodeUnprotected
		}
	}
	return domain.NewLadoVerifyItem(domain.LadoVerifyProtection, outcome, code, details)
}

// redactedErrorMax caps the characters of an error LADO stores or shows.
const redactedErrorMax = 300

// redactTokenPattern matches a bearer credential and the token shapes of
// GitHub and GitLab.
var redactTokenPattern = regexp.MustCompile(`(?i)\bbearer\s+\S+|\b(?:gh[pousr]_[0-9a-z]{20,}|github_pat_[0-9a-z_]{20,}|glpat-[0-9a-z_-]{20,})`)

// redactedErrorLine is err on one line with anything shaped like a token
// replaced, cut to redactedErrorMax characters.
func redactedErrorLine(err error) string {
	line := redactTokenPattern.ReplaceAllString(strings.Join(strings.Fields(err.Error()), " "), "[redacted]")
	if utf8.RuneCountInString(line) <= redactedErrorMax {
		return line
	}
	return string([]rune(line)[:redactedErrorMax]) + "…"
}
