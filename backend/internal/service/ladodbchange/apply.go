package ladodbchange

// Applying one SQL file: LADO reads the approved head's blob (never the path
// in a worktree, which may have moved on), writes it to a folder of its own
// under <StateDir>/lado/tmp, and runs the repository's command with {file}
// naming that copy. The command gets the daemon's environment without AO's
// variables and the tokens (ladoverify.CommandEnv), the project's variables,
// and then the repository's secrets. One command runs at a time in the whole
// daemon. A running row before and a result row after record what happened;
// the output is kept redacted, and the audit names the file and hashes only.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoverify"
)

// ApplyBlobMax caps the bytes of a SQL file LADO applies. A larger file
// fails without running.
const ApplyBlobMax = 32 << 20

// applyTarget is what one application needs besides the file: the run it
// belongs to, the repository's settings and folder, and who asked.
type applyTarget struct {
	run      domain.LadoDBChangeRun
	settings domain.LadoDBChangeSettings
	dir      string
	// actor is "lado" for the loop after an approval, or the developer's
	// presence actor for a run the developer queued.
	actor string
}

// applyResult is how one file's application ended: applied, or failed with
// a one-line message without secrets.
type applyResult struct {
	applied bool
	message string
}

// applyFile applies one SQL file of a run. It returns an error only when
// LADO could not record the application; a command that fails is a result.
func (s *Service) applyFile(ctx context.Context, target applyTarget, file domain.LadoDBChangeFileRef) (applyResult, error) {
	s.job.applyMu.Lock()
	defer s.job.applyMu.Unlock()

	secrets, err := s.Secrets(target.run.ProjectID, target.run.Repo)
	if err != nil {
		return applyResult{}, fmt.Errorf("read the secrets: %w", err)
	}
	if missing := missingNames(target.settings.SecretNames, secrets); len(missing) > 0 {
		// Nothing ran, so there is no application to record.
		return applyResult{message: "secrets without a value: " + strings.Join(missing, ", ")}, nil
	}
	env, err := s.commandEnv(ctx, target.run.ProjectID, secrets)
	if err != nil {
		return applyResult{}, err
	}

	app := domain.LadoDBChangeApplication{
		ID: s.deps.NewID(), ProjectID: target.run.ProjectID, Repo: target.run.Repo, Path: file.Path,
		BlobSHA: file.BlobSHA, Phase: file.Phase, Result: domain.LadoDBChangeResultRunning, RunID: target.run.ID,
		ExecutionSessionID: target.run.ExecutionSessionID, TaskID: target.run.TaskID, PRURL: target.run.PRURL,
		HeadSHA: target.run.HeadSHA, Actor: target.actor, StartedAt: s.now(),
	}
	if err := s.deps.Store.StartLadoDBChangeApplication(ctx, app); err != nil {
		return applyResult{}, fmt.Errorf("record the start of %s: %w", file.Path, err)
	}
	result, exitCode, output := s.runFile(ctx, target, file, app.ID, env, secrets)

	outputSHA := ""
	if len(output) > 0 && s.deps.Blobs != nil {
		if sum, err := s.deps.Blobs.Put(output); err != nil {
			s.deps.Log.Warn("LADO could not keep a SQL command's output", "path", file.Path, "err", err)
		} else {
			outputSHA = sum
		}
	}
	action := domain.LadoAuditDBChangeApplied
	if result.Result != domain.LadoDBChangeResultApplied {
		action = domain.LadoAuditDBChangeFailed
	}
	payload, _ := json.Marshal(map[string]any{
		"task": target.run.TaskID, "session": target.run.ExecutionSessionID, "pr": target.run.PRURL,
		"head": target.run.HeadSHA, "repo": target.run.Repo, "path": file.Path, "blob": file.BlobSHA,
		"phase": file.Phase, "exitCode": exitCode, "outputSha256": outputSHA,
	})
	now := s.now()
	audit := domain.LadoAuditEvent{
		At: now, Actor: target.actor, Action: action, SubjectKind: "execution",
		SubjectID: string(target.run.ExecutionSessionID), Payload: payload,
	}
	// The command ran: record its end even when the daemon stops meanwhile.
	if err := s.deps.Store.FinishLadoDBChangeApplication(context.WithoutCancel(ctx), app.ID, result.Result, exitCode, outputSHA, result.Message, now, audit); err != nil {
		return applyResult{}, fmt.Errorf("record the end of %s: %w", file.Path, err)
	}
	return applyResult{applied: result.Result == domain.LadoDBChangeResultApplied, message: result.Message}, nil
}

// fileOutcome is a finished command's result and message.
type fileOutcome struct {
	Result  domain.LadoDBChangeResult
	Message string
}

// runFile writes the blob to a folder of the application's own and runs the
// command on it. It returns the result, the exit code and the redacted end
// of the output. The folder is removed afterwards.
func (s *Service) runFile(ctx context.Context, target applyTarget, file domain.LadoDBChangeFileRef, id string, env []string, secrets map[string]string) (fileOutcome, *int, []byte) {
	fail := func(format string, args ...any) (fileOutcome, *int, []byte) {
		return fileOutcome{Result: domain.LadoDBChangeResultFailed, Message: RedactLine(fmt.Sprintf(format, args...), secrets)}, nil, nil
	}
	if !IsObjectID(file.BlobSHA) {
		return fail("not a blob id")
	}
	content, more, err := s.deps.Git.Blob(ctx, target.dir, file.BlobSHA, ApplyBlobMax)
	switch {
	case err != nil:
		return fail("read the file's blob: %v", err)
	case more:
		return fail("the file is larger than %d MiB", ApplyBlobMax>>20)
	}
	if phase := domain.LadoDBChangePhaseOf(string(content)); phase != file.Phase {
		return fail("the file's phase is %s, not %s", phase, file.Phase)
	}

	dir, err := s.applyDir(id)
	if err != nil {
		return fail("make the file's folder: %v", err)
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			s.deps.Log.Warn("LADO could not remove a SQL file's folder", "dir", dir, "err", err)
		}
	}()
	name := filepath.Join(dir, applyFileName(file.Path))
	if err := os.WriteFile(name, content, 0o600); err != nil {
		return fail("write the file: %v", err)
	}
	quoted, err := quoteShellPath(name, runtime.GOOS == "windows")
	if err != nil {
		return fail("%v", err)
	}
	command := strings.ReplaceAll(target.settings.Command, domain.LadoDBChangeFilePlaceholder, quoted)

	res := s.deps.Run(ctx, ladoverify.CommandSpec{Dir: dir, Command: command, Env: env, Timeout: target.settings.Timeout()})
	output := RedactOutput(res.Output, secrets)
	switch res.Status {
	case domain.LadoCheckRunPassed:
		return fileOutcome{Result: domain.LadoDBChangeResultApplied}, res.ExitCode, output
	case domain.LadoCheckRunCancelled:
		if ctx.Err() != nil {
			// The daemon stopped: the file may or may not be in the database.
			return fileOutcome{Result: domain.LadoDBChangeResultInterrupted, Message: "LADO stopped while the command ran"}, res.ExitCode, output
		}
	}
	return fileOutcome{Result: domain.LadoDBChangeResultFailed, Message: failureMessage(res, output, target.settings, secrets)}, res.ExitCode, output
}

// failureMessage is the one line of a command that did not pass: how it
// ended and the last line it printed.
func failureMessage(res ladoverify.CommandResult, output []byte, settings domain.LadoDBChangeSettings, secrets map[string]string) string {
	var head string
	switch {
	case res.Status == domain.LadoCheckRunTimedOut:
		head = fmt.Sprintf("timed out after %s", settings.Timeout())
	case res.ExitCode != nil:
		head = fmt.Sprintf("exit code %d", *res.ExitCode)
	case res.Err != nil:
		head = res.Err.Error()
	default:
		head = string(res.Status)
	}
	if last := lastLine(string(output)); last != "" {
		head += ": " + last
	}
	return RedactLine(head, secrets)
}

// lastLine is the last line of text that is not blank.
func lastLine(text string) string {
	lines := strings.Split(strings.TrimRight(text, "\r\n\t "), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}

// commandEnv is the command's environment: the daemon's without AO's
// variables and the tokens, then the project's variables, then the secrets,
// which replace any variable of the same name.
func (s *Service) commandEnv(ctx context.Context, projectID domain.ProjectID, secrets map[string]string) ([]string, error) {
	base := os.Environ()
	if s.deps.Env != nil {
		base = s.deps.Env()
	}
	project, ok, err := s.deps.Store.GetProject(ctx, string(projectID))
	switch {
	case err != nil:
		return nil, fmt.Errorf("read project %s: %w", projectID, err)
	case !ok:
		return nil, fmt.Errorf("project %s not found", projectID)
	}
	env := ladoverify.CommandEnv(base, project.Config.Env)
	names := make([]string, 0, len(secrets))
	for name := range secrets {
		names = append(names, name)
	}
	slices.Sort(names)
	env = slices.DeleteFunc(env, func(entry string) bool {
		name, _, _ := strings.Cut(entry, "=")
		return slices.ContainsFunc(names, func(secret string) bool { return strings.EqualFold(secret, name) })
	})
	for _, name := range names {
		env = append(env, name+"="+secrets[name])
	}
	return env, nil
}

// applyDir makes <StateDir>/lado/tmp/db-change-<id>, 0700.
func (s *Service) applyDir(id string) (string, error) {
	if s.deps.StateDir == "" {
		return "", errors.New("LADO has no state folder")
	}
	parent := filepath.Join(s.deps.StateDir, "lado", "tmp")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", err
	}
	dir := filepath.Join(parent, "db-change-"+safeName(id))
	if err := os.Mkdir(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// applyFileName is the name of a SQL file's copy: its own name with every
// character but letters, digits, dot, dash and underscore made "_", so the
// quoted path stays plain in every shell.
func applyFileName(repoPath string) string {
	name := safeName(path.Base(repoPath))
	if name == "" || name == "." || name == ".." {
		name = "change.sql"
	}
	return name
}

// safeName keeps letters, digits, '.', '-' and '_' of name and makes the
// rest '_'.
func safeName(name string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			return r
		}
		return '_'
	}, name)
}

// quoteShellPath quotes a path for the command's shell: in single quotes for
// sh, each single quote inside closed, escaped and reopened, and in double
// quotes for cmd, which cannot hold a double quote or a percent sign.
func quoteShellPath(p string, windows bool) (string, error) {
	if windows {
		if strings.ContainsAny(p, `"%`) {
			return "", fmt.Errorf("the path %q cannot be quoted for cmd", p)
		}
		return `"` + p + `"`, nil
	}
	return "'" + strings.ReplaceAll(p, "'", `'\''`) + "'", nil
}

// missingNames returns the names without a value in values, in order.
func missingNames(names []string, values map[string]string) []string {
	var missing []string
	for _, name := range names {
		if values[name] == "" {
			missing = append(missing, name)
		}
	}
	return missing
}
