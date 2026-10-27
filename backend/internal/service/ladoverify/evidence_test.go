package ladoverify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/ladoblob"
)

// LADO's blob store is what the daemon pins evidence files in.
var _ Blobs = (*ladoblob.Store)(nil)

func TestEvidenceParses(t *testing.T) {
	for _, c := range []struct {
		name    string
		content string
		want    EvidenceFile
		wantErr string
	}{
		{
			name: "a file in the prompt's form",
			content: "---\n" +
				"head: 0123456789abcdef0123456789abcdef01234567\n" +
				"criteria:\n" +
				"  - criterion: 1            # the 1-based number of the acceptance criterion in the approved plan\n" +
				"    evidence:\n" +
				"      - check: build\n" +
				"      - command: unit tests\n" +
				"      - commit: abc1234\n" +
				"    note: The timeout comes from the settings.\n" +
				"  - criterion: 2\n" +
				"    evidence:\n" +
				"      - commit: 1234567\n" +
				"  - criterion: 3\n" +
				"    note: Checked by hand.\n" +
				"---\n\n" +
				"# Evidence\n\nThe body is for people:\n\ncriteria:\n  - criterion: 4\n",
			want: EvidenceFile{
				Head: "0123456789abcdef0123456789abcdef01234567",
				Criteria: []EvidenceCriterion{
					{Criterion: 1, Evidence: []EvidenceEntry{{Check: "build"}, {Command: "unit tests"}, {Commit: "abc1234"}}, Note: "The timeout comes from the settings."},
					{Criterion: 2, Evidence: []EvidenceEntry{{Commit: "1234567"}}},
					{Criterion: 3, Note: "Checked by hand."},
				},
			},
		},
		{
			name: "unknown keys",
			content: "---\n" +
				"head: abc1234\n" +
				"model: claude-sonnet-5\n" +
				"criteria:\n" +
				"  - criterion: 1\n" +
				"    confidence: high\n" +
				"    evidence:\n" +
				"      - check: lint\n" +
				"        url: https://ci.example.com/runs/1\n" +
				"      - link: https://example.com/screenshot.png\n" +
				"    note: n\n" +
				"---\n",
			want: EvidenceFile{Head: "abc1234", Criteria: []EvidenceCriterion{{Criterion: 1, Evidence: []EvidenceEntry{{Check: "lint"}}, Note: "n"}}},
		},
		{name: "broken YAML", content: "---\nhead: [abc1234\ncriteria:\n  - criterion: 1\n---\n", wantErr: "front matter"},
		{name: "text where the criteria go", content: "---\ncriteria: all of them\n---\n", wantErr: "front matter"},
		{name: "plain text evidence entries", content: "---\ncriteria:\n  - criterion: 1\n    evidence:\n      - the build passed\n---\n", wantErr: "front matter"},
		{name: "front matter that is not a mapping", content: "---\n- head\n- criteria\n---\n", wantErr: "not a mapping"},
		{name: "empty front matter", content: "---\n---\n\nAll criteria are met.\n", wantErr: "empty"},
		{name: "no front matter", content: "# Evidence\n\nhead: abc1234\ncriteria:\n  - criterion: 1\n", wantErr: "no front matter"},
		{name: "not text", content: "---\nhead: \xff\xfe\n---\n", wantErr: "UTF-8"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseEvidence([]byte(c.content))
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("ParseEvidence = %+v, %v; want an error with %q", got, err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseEvidence: %v", err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("ParseEvidence = %+v\nwant %+v", got, c.want)
			}
		})
	}
}

func TestEvidenceResolvesAgainstRecords(t *testing.T) {
	head := strings.Repeat("c0ffee00", 5)
	other := strings.Repeat("deadbeef", 5)
	inRange := "2222222" + strings.Repeat("b", 33)
	fileSHA := strings.Repeat("e", 64)
	input := func(fileHead string) EvidenceInput {
		return EvidenceInput{
			File: &EvidenceFile{Head: fileHead, Criteria: []EvidenceCriterion{
				// A passed check, named without its case.
				{Criterion: 1, Evidence: []EvidenceEntry{{Check: "build"}}},
				// A failed check.
				{Criterion: 2, Evidence: []EvidenceEntry{{Check: "lint"}}},
				// A command whose newest passed run on the head is run-2.
				{Criterion: 3, Evidence: []EvidenceEntry{{Command: "unit tests"}}},
				// A commit of the branch by its short SHA, and a claim.
				{Criterion: 4, Evidence: []EvidenceEntry{{Commit: "2222222"}, {Check: "lint"}, {Commit: "2222222"}}},
				// A commit outside merge-base..head.
				{Criterion: 5, Evidence: []EvidenceEntry{{Commit: "3333333"}}},
				// Criterion 6 is not listed; criterion 7 only has a note.
				{Criterion: 7, Note: "Checked by hand."},
				// Numbers outside the plan are ignored.
				{Criterion: 9, Evidence: []EvidenceEntry{{Check: "build"}}},
				{Criterion: 0, Evidence: []EvidenceEntry{{Check: "build"}}},
			}},
			FileRef:       &EvidenceFileRef{Session: "app-7", Path: domain.LadoEvidencePath, Version: 3, SHA256: fileSHA},
			CriteriaCount: 7,
			HeadSHA:       head,
			Checks: []domain.PullRequestCheck{
				{Name: "Build", CommitHash: head, Status: domain.PRCheckPassed},
				{Name: "lint", CommitHash: head, Status: domain.PRCheckFailed},
			},
			Runs: []domain.LadoCheckRun{
				{ID: "run-1", Name: "unit tests", HeadSHA: head, Status: domain.LadoCheckRunFailed, StartedAt: t0},
				{ID: "run-2", Name: "unit tests", HeadSHA: head, Status: domain.LadoCheckRunPassed, StartedAt: t0.Add(time.Minute)},
				// A run of another head does not count, however new.
				{ID: "run-3", Name: "unit tests", HeadSHA: other, Status: domain.LadoCheckRunPassed, StartedAt: t0.Add(time.Hour)},
			},
			Commits: []string{"1111111" + strings.Repeat("a", 33), inRange, head},
		}
	}
	fileLink := domain.LadoEvidenceLink{Criterion: 0, State: domain.LadoEvidenceFile, RefKind: domain.LadoEvidenceRefArtifactVersion,
		RefKey: "app-7/lado/evidence.md@3", SHA: fileSHA}
	inferred := func(index int) domain.LadoEvidenceLink {
		return domain.LadoEvidenceLink{Criterion: index, State: domain.LadoEvidenceInferred, RefKind: domain.LadoEvidenceRefNone}
	}
	noted := domain.LadoEvidenceLink{Criterion: 7, State: domain.LadoEvidenceClaimed, RefKind: domain.LadoEvidenceRefNone}
	build := domain.LadoEvidenceRef{Kind: domain.LadoEvidenceRefPRCheck, Key: "Build", SHA: head}
	lintClaim := domain.LadoEvidenceRef{Kind: domain.LadoEvidenceRefClaim, Key: "check: lint"}
	unitTests := domain.LadoEvidenceRef{Kind: domain.LadoEvidenceRefCheckRun, Key: "run-2", SHA: head}
	commit := domain.LadoEvidenceRef{Kind: domain.LadoEvidenceRefCommit, Key: inRange, SHA: inRange}
	commitClaim := domain.LadoEvidenceRef{Kind: domain.LadoEvidenceRefClaim, Key: "commit: 3333333"}
	criterion := func(index int, state domain.LadoEvidenceState, refs ...domain.LadoEvidenceRef) domain.LadoEvidenceCriterion {
		return domain.LadoEvidenceCriterion{Index: index, State: state, Refs: refs}
	}
	linksOf := func(criteria ...domain.LadoEvidenceCriterion) []domain.LadoEvidenceLink {
		var links []domain.LadoEvidenceLink
		for _, c := range criteria {
			if len(c.Refs) == 0 {
				links = append(links, domain.LadoEvidenceLink{Criterion: c.Index, State: c.State, RefKind: domain.LadoEvidenceRefNone})
			}
			for _, ref := range c.Refs {
				links = append(links, domain.LadoEvidenceLink{Criterion: c.Index, State: c.State, RefKind: ref.Kind, RefKey: ref.Key, SHA: ref.SHA})
			}
		}
		return links
	}

	for _, c := range []struct {
		name      string
		in        EvidenceInput
		wantLinks []domain.LadoEvidenceLink
		want      domain.LadoEvidenceDetails
	}{
		{
			name: "a file for the head",
			in:   input(head),
			wantLinks: append([]domain.LadoEvidenceLink{fileLink}, append(linksOf(
				criterion(1, domain.LadoEvidenceLinked, build),
				criterion(2, domain.LadoEvidenceClaimed, lintClaim),
				criterion(3, domain.LadoEvidenceLinked, unitTests),
				criterion(4, domain.LadoEvidenceLinked, commit, lintClaim),
				criterion(5, domain.LadoEvidenceClaimed, commitClaim),
			), inferred(6), noted)...),
			want: domain.LadoEvidenceDetails{FileVersion: 3, FileSHA256: fileSHA, FileHead: head, Criteria: []domain.LadoEvidenceCriterion{
				criterion(1, domain.LadoEvidenceLinked, build),
				criterion(2, domain.LadoEvidenceClaimed, lintClaim),
				criterion(3, domain.LadoEvidenceLinked, unitTests),
				criterion(4, domain.LadoEvidenceLinked, commit, lintClaim),
				criterion(5, domain.LadoEvidenceClaimed, commitClaim),
				criterion(6, domain.LadoEvidenceInferred),
				criterion(7, domain.LadoEvidenceClaimed),
			}},
		},
		{
			name: "a file that names the head by a prefix",
			in:   input(strings.ToUpper(head[:12])),
			wantLinks: append([]domain.LadoEvidenceLink{fileLink}, append(linksOf(
				criterion(1, domain.LadoEvidenceLinked, build),
				criterion(2, domain.LadoEvidenceClaimed, lintClaim),
				criterion(3, domain.LadoEvidenceLinked, unitTests),
				criterion(4, domain.LadoEvidenceLinked, commit, lintClaim),
				criterion(5, domain.LadoEvidenceClaimed, commitClaim),
			), inferred(6), noted)...),
			want: domain.LadoEvidenceDetails{FileVersion: 3, FileSHA256: fileSHA, FileHead: strings.ToUpper(head[:12]), Criteria: []domain.LadoEvidenceCriterion{
				criterion(1, domain.LadoEvidenceLinked, build),
				criterion(2, domain.LadoEvidenceClaimed, lintClaim),
				criterion(3, domain.LadoEvidenceLinked, unitTests),
				criterion(4, domain.LadoEvidenceLinked, commit, lintClaim),
				criterion(5, domain.LadoEvidenceClaimed, commitClaim),
				criterion(6, domain.LadoEvidenceInferred),
				criterion(7, domain.LadoEvidenceClaimed),
			}},
		},
		{
			name: "a file for another head",
			in:   input(other),
			wantLinks: append([]domain.LadoEvidenceLink{fileLink}, append(linksOf(
				criterion(1, domain.LadoEvidenceStale, build),
				criterion(2, domain.LadoEvidenceClaimed, lintClaim),
				criterion(3, domain.LadoEvidenceStale, unitTests),
				criterion(4, domain.LadoEvidenceStale, commit, lintClaim),
				criterion(5, domain.LadoEvidenceClaimed, commitClaim),
			), inferred(6), noted)...),
			want: domain.LadoEvidenceDetails{FileVersion: 3, FileSHA256: fileSHA, FileHead: other, Criteria: []domain.LadoEvidenceCriterion{
				criterion(1, domain.LadoEvidenceStale, build),
				criterion(2, domain.LadoEvidenceClaimed, lintClaim),
				criterion(3, domain.LadoEvidenceStale, unitTests),
				criterion(4, domain.LadoEvidenceStale, commit, lintClaim),
				criterion(5, domain.LadoEvidenceClaimed, commitClaim),
				criterion(6, domain.LadoEvidenceInferred),
				criterion(7, domain.LadoEvidenceClaimed),
			}},
		},
		{
			name: "a pinned file that could not be parsed",
			in: func() EvidenceInput {
				in := input(head)
				in.File, in.CriteriaCount = nil, 2
				return in
			}(),
			wantLinks: []domain.LadoEvidenceLink{fileLink, inferred(1), inferred(2)},
			want: domain.LadoEvidenceDetails{FileVersion: 3, FileSHA256: fileSHA, Criteria: []domain.LadoEvidenceCriterion{
				criterion(1, domain.LadoEvidenceInferred), criterion(2, domain.LadoEvidenceInferred),
			}},
		},
		{
			name:      "no file",
			in:        EvidenceInput{CriteriaCount: 2, HeadSHA: head},
			wantLinks: []domain.LadoEvidenceLink{inferred(1), inferred(2)},
			want: domain.LadoEvidenceDetails{Criteria: []domain.LadoEvidenceCriterion{
				criterion(1, domain.LadoEvidenceInferred), criterion(2, domain.LadoEvidenceInferred),
			}},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			links, item, err := ResolveEvidence(c.in)
			if err != nil {
				t.Fatalf("ResolveEvidence: %v", err)
			}
			if !reflect.DeepEqual(links, c.wantLinks) {
				t.Fatalf("links =\n%+v\nwant\n%+v", links, c.wantLinks)
			}
			want, err := domain.NewLadoVerifyItem(domain.LadoVerifyEvidence, domain.LadoVerifyInfo, "", c.want)
			if err != nil {
				t.Fatalf("NewLadoVerifyItem: %v", err)
			}
			if item.Kind != domain.LadoVerifyEvidence || item.Outcome != domain.LadoVerifyInfo || item.Code != "" ||
				string(item.Details) != string(want.Details) {
				t.Fatalf("item = %s %s %q %s\nwant evidence info with %s", item.Kind, item.Outcome, item.Code, item.Details, want.Details)
			}
		})
	}
}

func TestEvidencePinChecksTheHash(t *testing.T) {
	ctx := context.Background()
	const session = domain.SessionID("app-7")
	first := []byte("---\nhead: 1111111\ncriteria: []\n---\n")
	second := []byte("---\nhead: 2222222\ncriteria:\n  - criterion: 1\n    evidence:\n      - check: build\n---\n")
	version := func(n int64, content []byte) domain.SessionArtifactVersion {
		return domain.SessionArtifactVersion{SessionID: session, Path: domain.LadoEvidencePath, Version: n,
			SHA256: evidenceSHA(content), Size: int64(len(content))}
	}

	t.Run("bytes that do not match AO's record", func(t *testing.T) {
		blobs := ladoblob.New(t.TempDir())
		// The agent rewrote AO's copy of version 2 after AO recorded it.
		artifacts := &evidenceArtifacts{
			versions: []domain.SessionArtifactVersion{version(1, first), version(2, second)},
			content:  map[int64][]byte{1: first, 2: []byte("---\nhead: 2222222\ncriteria: []\n---\n")},
		}
		ref, data, err := PinEvidence(ctx, artifacts, blobs, session)
		if !errors.Is(err, ErrEvidenceHashMismatch) || ref != nil || data != nil {
			t.Fatalf("PinEvidence = %+v, %q, %v; want ErrEvidenceHashMismatch", ref, data, err)
		}
		for _, content := range [][]byte{first, second, artifacts.content[2]} {
			if _, err := blobs.Get(evidenceSHA(content), EvidenceMaxBytes); !errors.Is(err, ladoblob.ErrNotFound) {
				t.Fatalf("blob of %q: %v, want none pinned", content, err)
			}
		}
	})

	t.Run("the newest version", func(t *testing.T) {
		blobs := ladoblob.New(t.TempDir())
		artifacts := &evidenceArtifacts{
			versions: []domain.SessionArtifactVersion{version(2, second), version(1, first)},
			content:  map[int64][]byte{1: first, 2: second},
		}
		ref, data, err := PinEvidence(ctx, artifacts, blobs, session)
		if err != nil {
			t.Fatalf("PinEvidence: %v", err)
		}
		want := EvidenceFileRef{Session: session, Path: domain.LadoEvidencePath, Version: 2, SHA256: evidenceSHA(second)}
		if ref == nil || *ref != want || string(data) != string(second) {
			t.Fatalf("PinEvidence = %+v, %q; want %+v with the second version's bytes", ref, data, want)
		}
		if pinned, err := blobs.Get(evidenceSHA(second), EvidenceMaxBytes); err != nil || string(pinned) != string(second) {
			t.Fatalf("pinned blob = %q, %v; want the second version", pinned, err)
		}
		if !reflect.DeepEqual(artifacts.asked, []string{"app-7 lado/evidence.md"}) || !reflect.DeepEqual(artifacts.read, []int64{2}) {
			t.Fatalf("asked for %v and read %v, want the evidence path of app-7 and version 2 only", artifacts.asked, artifacts.read)
		}
	})

	t.Run("a file over the limit", func(t *testing.T) {
		blobs := ladoblob.New(t.TempDir())
		big := version(1, first)
		big.Size = EvidenceMaxBytes + 1
		artifacts := &evidenceArtifacts{versions: []domain.SessionArtifactVersion{big}, content: map[int64][]byte{1: first}}
		ref, data, err := PinEvidence(ctx, artifacts, blobs, session)
		if ref != nil || data != nil || err != nil || len(artifacts.read) != 0 {
			t.Fatalf("PinEvidence = %+v, %q, %v after reading %v; want nothing read or pinned", ref, data, err, artifacts.read)
		}
	})

	t.Run("no evidence file", func(t *testing.T) {
		blobs := ladoblob.New(t.TempDir())
		ref, data, err := PinEvidence(ctx, &evidenceArtifacts{}, blobs, session)
		if ref != nil || data != nil || err != nil {
			t.Fatalf("PinEvidence = %+v, %q, %v; want nil, nil, nil", ref, data, err)
		}
	})

	t.Run("versions that cannot be listed", func(t *testing.T) {
		blobs := ladoblob.New(t.TempDir())
		ref, _, err := PinEvidence(ctx, &evidenceArtifacts{err: errors.New("artifact versions: database is locked")}, blobs, session)
		if ref != nil || err == nil || !strings.Contains(err.Error(), "database is locked") {
			t.Fatalf("PinEvidence = %+v, %v; want the listing error", ref, err)
		}
	})
}

func evidenceSHA(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// evidenceArtifacts is AO's artifact version store for one session: the
// versions it recorded, and the bytes its copies hold now.
type evidenceArtifacts struct {
	versions []domain.SessionArtifactVersion
	content  map[int64][]byte
	err      error
	asked    []string
	read     []int64
}

func (a *evidenceArtifacts) Versions(_ context.Context, session domain.SessionID, path string) ([]domain.SessionArtifactVersion, error) {
	a.asked = append(a.asked, string(session)+" "+path)
	if a.err != nil {
		return nil, a.err
	}
	var versions []domain.SessionArtifactVersion
	for _, v := range a.versions {
		if v.SessionID == session && v.Path == path {
			versions = append(versions, v)
		}
	}
	return versions, nil
}

func (a *evidenceArtifacts) Read(_ context.Context, version domain.SessionArtifactVersion) ([]byte, error) {
	a.read = append(a.read, version.Version)
	content, ok := a.content[version.Version]
	if !ok {
		return nil, errors.New("no such version")
	}
	return content, nil
}
