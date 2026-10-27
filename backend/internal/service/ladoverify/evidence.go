package ladoverify

// This file holds the evidence item (docs/LJY-PRD/TDD.md §8.6, PRD EVD-001
// and EVD-002). The worker names, in lado/evidence.md in its session
// artifact directory, the records that show each acceptance criterion of
// the approved plan is met; LADO pins the file and checks every entry
// against the records of the head. Evidence never blocks the integration:
// the developer sees what LADO could link and what stays a claim.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// EvidenceMaxBytes is the largest evidence file LADO pins; a larger one is
// ignored.
const EvidenceMaxBytes = 1 << 20

const (
	// evidenceMaxRefs caps the records one criterion lists and
	// evidenceMaxTextRunes the text kept of a claim or a head, so a long
	// file cannot swell the record.
	evidenceMaxRefs      = 20
	evidenceMaxTextRunes = 200
	// evidenceMinSHAChars is the shortest commit prefix an entry may name.
	evidenceMinSHAChars = 7
)

// ErrEvidenceHashMismatch means the bytes read for the newest version of an
// evidence file do not hash to the sha256 AO recorded for it.
var ErrEvidenceHashMismatch = errors.New("the evidence file does not match AO's record of it")

// EvidenceEntry is one record an evidence entry names: a CI check of the
// pull request, a LADO check command or a commit of the branch. An entry
// sets at least one.
type EvidenceEntry struct {
	Check   string
	Command string
	Commit  string
}

// EvidenceCriterion is what the evidence file says about one acceptance
// criterion, by its 1-based number in the approved plan.
type EvidenceCriterion struct {
	Criterion int
	Evidence  []EvidenceEntry
	Note      string
}

// EvidenceFile is the front matter of an evidence file: the head it
// describes and its criteria.
type EvidenceFile struct {
	Head     string
	Criteria []EvidenceCriterion
}

// EvidenceFileRef is the pinned version of a session's evidence file.
type EvidenceFileRef struct {
	Session domain.SessionID
	Path    string
	Version int
	SHA256  string
}

// key is the ref key of the file's link, "<session>/<path>@<version>".
func (r EvidenceFileRef) key() string {
	return string(r.Session) + "/" + r.Path + "@" + strconv.Itoa(r.Version)
}

// ParseEvidence reads an evidence file's YAML front matter; the Markdown
// after it is for people and is not read. Keys LADO does not know are
// ignored, and so is an entry that names no record. A file without front
// matter, or whose front matter does not have the format's shape, is an
// error: the caller then treats every criterion as inferred.
func ParseEvidence(data []byte) (EvidenceFile, error) {
	if !utf8.Valid(data) {
		return EvidenceFile{}, errors.New("the evidence file is not UTF-8 text")
	}
	front, _, ok := domain.SplitLadoFrontMatter(string(data))
	if !ok {
		return EvidenceFile{}, errors.New("the evidence file has no front matter")
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(front), &doc); err != nil {
		return EvidenceFile{}, fmt.Errorf("the evidence file's front matter: %w", err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return EvidenceFile{}, errors.New("the evidence file's front matter is empty or not a mapping")
	}
	var raw struct {
		Head     string `yaml:"head"`
		Criteria []struct {
			Criterion int `yaml:"criterion"`
			Evidence  []struct {
				Check   string `yaml:"check"`
				Command string `yaml:"command"`
				Commit  string `yaml:"commit"`
			} `yaml:"evidence"`
			Note string `yaml:"note"`
		} `yaml:"criteria"`
	}
	if err := doc.Decode(&raw); err != nil {
		return EvidenceFile{}, fmt.Errorf("the evidence file's front matter: %w", err)
	}
	file := EvidenceFile{Head: strings.TrimSpace(raw.Head)}
	for _, c := range raw.Criteria {
		criterion := EvidenceCriterion{Criterion: c.Criterion, Note: strings.TrimSpace(c.Note)}
		for _, e := range c.Evidence {
			entry := EvidenceEntry{Check: strings.TrimSpace(e.Check), Command: strings.TrimSpace(e.Command), Commit: strings.TrimSpace(e.Commit)}
			if entry != (EvidenceEntry{}) {
				criterion.Evidence = append(criterion.Evidence, entry)
			}
		}
		file.Criteria = append(file.Criteria, criterion)
	}
	return file, nil
}

// Artifacts reads AO's artifact versions, as ladotask's plan watcher does.
type Artifacts interface {
	// Versions lists a session's versions of one artifact path.
	Versions(ctx context.Context, sessionID domain.SessionID, path string) ([]domain.SessionArtifactVersion, error)
	// Read returns one version's content.
	Read(ctx context.Context, version domain.SessionArtifactVersion) ([]byte, error)
}

// Blobs is LADO's content store (*ladoblob.Store): files named by the
// sha256 of their bytes.
type Blobs interface {
	Put(data []byte) (string, error)
	Get(sum string, limit int64) ([]byte, error)
}

// PinEvidence pins the newest version AO captured of the session's evidence
// file: it reads the version's bytes, checks them against the sha256 AO
// recorded and copies them to LADO's blob store, so the file the links came
// from outlives AO's pruning. It returns the pinned version and its bytes,
// or nil, nil, nil when the session has no evidence file or the newest is
// larger than EvidenceMaxBytes. Bytes that do not match AO's record are
// refused with ErrEvidenceHashMismatch.
func PinEvidence(ctx context.Context, artifacts Artifacts, blobs Blobs, session domain.SessionID) (*EvidenceFileRef, []byte, error) {
	versions, err := artifacts.Versions(ctx, session, domain.LadoEvidencePath)
	if err != nil {
		return nil, nil, fmt.Errorf("list the evidence file's versions: %w", err)
	}
	if len(versions) == 0 {
		return nil, nil, nil
	}
	newest := versions[0]
	for _, version := range versions[1:] {
		if version.Version > newest.Version {
			newest = version
		}
	}
	if newest.Size > EvidenceMaxBytes {
		return nil, nil, nil
	}
	content, err := artifacts.Read(ctx, newest)
	if err != nil {
		return nil, nil, fmt.Errorf("read version %d of the evidence file: %w", newest.Version, err)
	}
	if len(content) > EvidenceMaxBytes {
		return nil, nil, nil
	}
	sum := sha256.Sum256(content)
	if hex.EncodeToString(sum[:]) != newest.SHA256 {
		// The agent can write to AO's version store; a copy that no longer
		// matches AO's record is not the version AO captured.
		return nil, nil, fmt.Errorf("version %d: %w", newest.Version, ErrEvidenceHashMismatch)
	}
	if _, err := blobs.Put(content); err != nil {
		return nil, nil, fmt.Errorf("pin the evidence file: %w", err)
	}
	ref := &EvidenceFileRef{Session: session, Path: domain.LadoEvidencePath, Version: int(newest.Version), SHA256: newest.SHA256}
	return ref, content, nil
}

// EvidenceInput is what the evidence of one head is checked against.
type EvidenceInput struct {
	// File is the parsed evidence file, nil when there is none or it could
	// not be parsed; FileRef its pinned version, nil when there is none.
	File    *EvidenceFile
	FileRef *EvidenceFileRef
	// CriteriaCount is how many acceptance criteria the approved plan has.
	CriteriaCount int
	HeadSHA       string
	// Checks are the PR's checks of the head, Runs LADO's check command runs
	// of the head, and Commits the commits merge-base..head.
	Checks  []domain.PullRequestCheck
	Runs    []domain.LadoCheckRun
	Commits []string
}

// ResolveEvidence checks every entry of the evidence file against the
// records of the head (decision k of the step 8 plan): a check names a
// passed check of the head, a command a passed run of a LADO check command
// on the head, a commit one of merge-base..head (by at least its first 7
// characters); any other entry is a claim. A criterion with a resolved entry
// is linked, or stale when the file describes another head; one with
// entries but none resolved is claimed; one the file does not list is
// inferred. Numbers outside the plan's criteria are ignored.
//
// It returns the links to store for the head, criterion 0 for the pinned
// file and one per record of each criterion (ref none for a criterion
// without entries), and the evidence item, always info. The links carry the
// criterion, state and record: the caller gives each its ID and time, and
// the store takes the execution and head from its arguments.
func ResolveEvidence(in EvidenceInput) ([]domain.LadoEvidenceLink, domain.LadoVerifyItem, error) {
	var details domain.LadoEvidenceDetails
	var links []domain.LadoEvidenceLink
	if in.FileRef != nil {
		details.FileVersion, details.FileSHA256 = in.FileRef.Version, in.FileRef.SHA256
		links = append(links, domain.LadoEvidenceLink{
			Criterion: 0, State: domain.LadoEvidenceFile,
			RefKind: domain.LadoEvidenceRefArtifactVersion, RefKey: in.FileRef.key(), SHA: in.FileRef.SHA256,
		})
	}
	listed := map[int][]EvidenceEntry{}
	sameHead := false
	if in.File != nil {
		details.FileHead = evidenceText(in.File.Head)
		sameHead = evidenceSameCommit(in.File.Head, in.HeadSHA)
		for _, c := range in.File.Criteria {
			if c.Criterion >= 1 && c.Criterion <= in.CriteriaCount {
				listed[c.Criterion] = append(listed[c.Criterion], c.Evidence...)
			}
		}
	}
	for index := 1; index <= in.CriteriaCount; index++ {
		criterion := domain.LadoEvidenceCriterion{Index: index, State: domain.LadoEvidenceInferred}
		if entries, ok := listed[index]; ok {
			refs, resolved := evidenceRefs(in, entries)
			switch {
			case resolved && sameHead:
				criterion.State = domain.LadoEvidenceLinked
			case resolved:
				criterion.State = domain.LadoEvidenceStale
			default:
				criterion.State = domain.LadoEvidenceClaimed
			}
			criterion.Refs = refs
		}
		details.Criteria = append(details.Criteria, criterion)
		if len(criterion.Refs) == 0 {
			links = append(links, domain.LadoEvidenceLink{Criterion: index, State: criterion.State, RefKind: domain.LadoEvidenceRefNone})
		}
		for _, ref := range criterion.Refs {
			links = append(links, domain.LadoEvidenceLink{Criterion: index, State: criterion.State, RefKind: ref.Kind, RefKey: ref.Key, SHA: ref.SHA})
		}
	}
	item, err := domain.NewLadoVerifyItem(domain.LadoVerifyEvidence, domain.LadoVerifyInfo, "", details)
	if err != nil {
		return nil, domain.LadoVerifyItem{}, err
	}
	return links, item, nil
}

// evidenceRefs resolves a criterion's entries in the file's order, without
// repeats, and reports whether any resolved to a record.
func evidenceRefs(in EvidenceInput, entries []EvidenceEntry) ([]domain.LadoEvidenceRef, bool) {
	var refs []domain.LadoEvidenceRef
	resolved := false
	add := func(ref domain.LadoEvidenceRef) {
		if len(refs) >= evidenceMaxRefs || slices.Contains(refs, ref) {
			return
		}
		refs = append(refs, ref)
		resolved = resolved || ref.Kind != domain.LadoEvidenceRefClaim
	}
	for _, entry := range entries {
		if entry.Check != "" {
			add(evidenceCheckRef(in, entry.Check))
		}
		if entry.Command != "" {
			add(evidenceCommandRef(in, entry.Command))
		}
		if entry.Commit != "" {
			add(evidenceCommitRef(in, entry.Commit))
		}
	}
	return refs, resolved
}

// evidenceCheckRef resolves a CI check name, compared without case, to a
// passed check of the head.
func evidenceCheckRef(in EvidenceInput, name string) domain.LadoEvidenceRef {
	for _, check := range in.Checks {
		if check.Status == domain.PRCheckPassed && strings.EqualFold(strings.TrimSpace(check.Name), name) &&
			(check.CommitHash == "" || evidenceSameCommit(check.CommitHash, in.HeadSHA)) {
			return domain.LadoEvidenceRef{Kind: domain.LadoEvidenceRefPRCheck, Key: check.Name, SHA: in.HeadSHA}
		}
	}
	return evidenceClaim("check", name)
}

// evidenceCommandRef resolves a LADO check command name, compared without
// case, to the newest passed run of the command on the head.
func evidenceCommandRef(in EvidenceInput, name string) domain.LadoEvidenceRef {
	var found *domain.LadoCheckRun
	for i, run := range in.Runs {
		if run.Status == domain.LadoCheckRunPassed && strings.EqualFold(run.Name, name) &&
			evidenceSameCommit(run.HeadSHA, in.HeadSHA) && (found == nil || !run.StartedAt.Before(found.StartedAt)) {
			found = &in.Runs[i]
		}
	}
	if found == nil {
		return evidenceClaim("command", name)
	}
	return domain.LadoEvidenceRef{Kind: domain.LadoEvidenceRefCheckRun, Key: found.ID, SHA: in.HeadSHA}
}

// evidenceCommitRef resolves a commit SHA, or a prefix of at least 7
// characters that names one commit, to a commit of merge-base..head.
func evidenceCommitRef(in EvidenceInput, sha string) domain.LadoEvidenceRef {
	found := ""
	for _, commit := range in.Commits {
		if evidenceSameCommit(sha, commit) {
			if found != "" && found != commit {
				return evidenceClaim("commit", sha)
			}
			found = commit
		}
	}
	if found == "" {
		return evidenceClaim("commit", sha)
	}
	return domain.LadoEvidenceRef{Kind: domain.LadoEvidenceRefCommit, Key: found, SHA: found}
}

// evidenceSameCommit reports whether named, a commit SHA or a prefix of at
// least 7 hex characters, names the commit full.
func evidenceSameCommit(named, full string) bool {
	named, full = strings.TrimSpace(named), strings.TrimSpace(full)
	if len(named) < evidenceMinSHAChars || len(named) > len(full) {
		return false
	}
	for _, r := range named {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}
	return strings.EqualFold(full[:len(named)], named)
}

// evidenceClaim is an entry that matched no record: its kind and text as the
// file gives them.
func evidenceClaim(kind, text string) domain.LadoEvidenceRef {
	return domain.LadoEvidenceRef{Kind: domain.LadoEvidenceRefClaim, Key: kind + ": " + evidenceText(text)}
}

// evidenceText is text from the file on one line, cut to
// evidenceMaxTextRunes.
func evidenceText(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(text) > evidenceMaxTextRunes {
		text = string([]rune(text)[:evidenceMaxTextRunes]) + "…"
	}
	return text
}
