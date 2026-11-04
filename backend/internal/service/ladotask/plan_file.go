package ladotask

// The approved plan as a file a worker can read again (issue #42, plan
// decision 3). A worker whose context is summarized loses the plan its prompt
// carried, so RunInput keeps the pinned plan, read-only, at
// <DataDir>/lado/runs/<revision-id>/plan.md, outside every worktree, and the
// prompts name it. Every run of a revision shares the file. RunInput runs
// again for each part, merge and integration worker, so the file is written
// only when it is missing and compared otherwise.

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// PlanFileName is the name of the pinned plan in a run's folder.
const PlanFileName = "plan.md"

// RunsDir is the folder of a revision's runs: <DataDir>/lado/runs/<revision>.
// It is empty without a data dir or for a revision id that is not one path
// segment.
func RunsDir(dataDir string, rev domain.LadoPlanRevisionID) string {
	id := string(rev)
	if dataDir == "" || id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\:`) {
		return ""
	}
	return filepath.Join(dataDir, "lado", "runs", id)
}

// writePlanFile keeps content, the pinned plan of rev, in rev's runs folder
// and returns the file's path. A file already there must hold the same bytes:
// it is compared by sha256 and never written again, as it is read-only.
func writePlanFile(dataDir string, rev domain.LadoPlanRevisionID, content []byte) (string, error) {
	dir := RunsDir(dataDir, rev)
	if dir == "" {
		return "", fmt.Errorf("no runs folder for plan revision %q", rev)
	}
	path := filepath.Join(dir, PlanFileName)
	want := sha256.Sum256(content)
	if ok, err := planFileHolds(path, want); err == nil {
		if !ok {
			return "", fmt.Errorf("the plan file of revision %s does not match its pinned plan", rev)
		}
		return path, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("read the plan file of revision %s: %w", rev, err)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("make the runs folder of revision %s: %w", rev, err)
	}
	tmp, err := os.CreateTemp(dir, ".plan-*.tmp")
	if err != nil {
		return "", fmt.Errorf("write the plan file of revision %s: %w", rev, err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // nothing once renamed
	_, err = tmp.Write(content)
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Chmod(tmpName, 0o400)
	}
	if err == nil {
		err = os.Rename(tmpName, path)
	}
	if err != nil {
		// Another run of the revision may have put the file there first.
		if ok, readErr := planFileHolds(path, want); readErr == nil && ok {
			return path, nil
		}
		return "", fmt.Errorf("write the plan file of revision %s: %w", rev, err)
	}
	return path, nil
}

// planFileHolds reports whether the file at path holds bytes whose sha256 is
// want. A missing file is fs.ErrNotExist.
func planFileHolds(path string, want [sha256.Size]byte) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	return sha256.Sum256(data) == want, nil
}

// PlanDirStore is what PlanDirResolver reads.
type PlanDirStore interface {
	LadoExecution(ctx context.Context, sessionID domain.SessionID) (domain.LadoExecution, bool, error)
	ListLadoTasks(ctx context.Context, projectID domain.ProjectID, includeCancelled bool) ([]domain.LadoTask, error)
}

// PlanDirResolver finds the runs folder of a LADO run's session, which the
// session manager lets the session's Chat read without asking
// (sessionmanager.LadoReadDirectories).
type PlanDirResolver struct {
	store   PlanDirStore
	dataDir string
}

// NewPlanDirResolver returns a resolver over store for AO's data dir.
func NewPlanDirResolver(store PlanDirStore, dataDir string) *PlanDirResolver {
	return &PlanDirResolver{store: store, dataDir: dataDir}
}

// AdditionalDirectories returns the runs folder of rec's plan revision when
// rec is a worker of a LADO run (a single, part, merge or integration
// worker) and the folder holds the plan file, and nil otherwise. It finds
// the revision on the session's execution, or before the execution is linked
// (the first start of the session) on the newest approval of the task whose
// queue entry the session's client request id names.
func (r *PlanDirResolver) AdditionalDirectories(ctx context.Context, rec domain.SessionRecord) ([]string, error) {
	if r == nil || r.store == nil || r.dataDir == "" {
		return nil, nil
	}
	run, ok := domain.ParseLadoRunClientRequestID(rec.ClientRequestID)
	if !ok {
		return nil, nil
	}
	rev, err := r.revision(ctx, rec, run)
	if err != nil || rev == "" {
		return nil, err
	}
	dir := RunsDir(r.dataDir, rev)
	if dir == "" {
		return nil, nil
	}
	if !regularFile(filepath.Join(dir, PlanFileName)) {
		return nil, nil
	}
	return []string{dir}, nil
}

// regularFile reports whether path names a regular file, links followed.
func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func (r *PlanDirResolver) revision(ctx context.Context, rec domain.SessionRecord, run domain.LadoRunRequest) (domain.LadoPlanRevisionID, error) {
	exec, found, err := r.store.LadoExecution(ctx, rec.ID)
	if err != nil {
		return "", fmt.Errorf("read the execution of session %s: %w", rec.ID, err)
	}
	if found && exec.PlanRevisionID != "" {
		return exec.PlanRevisionID, nil
	}
	tasks, err := r.store.ListLadoTasks(ctx, rec.ProjectID, false)
	if err != nil {
		return "", fmt.Errorf("list the LADO tasks of project %s: %w", rec.ProjectID, err)
	}
	for _, task := range tasks {
		if task.Queue.ID != run.Entry {
			continue
		}
		// The entry is the newest approval's, which every worker of its run
		// starts from (RunInput).
		if task.Plan.Approval.ID == "" || task.Plan.Approval.ID != task.Queue.PlanApprovalID {
			return "", nil
		}
		return task.Plan.Approval.RevisionID, nil
	}
	return "", nil
}
