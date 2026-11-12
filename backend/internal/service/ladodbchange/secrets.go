package ladodbchange

// The secrets of a repository's database change settings: the values of the
// environment variables its command gets, such as a connection string. They
// live in one file per repository under <StateDir>/lado/credentials/db, 0600
// in a 0700 folder and replaced atomically, as LADO keeps its GitLab tokens.
// Nothing else holds a value: the settings, the views, the audit and the
// logs name the secrets only.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// SecretValueMax caps a secret's value in bytes.
const SecretValueMax = 8 << 10

// secretsFile is one repository's secrets file.
type secretsFile struct {
	Version   int               `json:"version"`
	ProjectID domain.ProjectID  `json:"projectId"`
	Repo      string            `json:"repo"`
	Secrets   map[string]string `json:"secrets"`
}

// secretsDir is the folder of the secrets files.
func (s *Service) secretsDir() string {
	return filepath.Join(s.deps.StateDir, "lado", "credentials", "db")
}

// secretsPath is the secrets file of a repository: its name is the first 16
// hex digits of sha256(project NUL repo), so no folder name of the
// repository reaches the path.
func (s *Service) secretsPath(project domain.ProjectID, repo string) string {
	sum := sha256.Sum256([]byte(string(project) + "\x00" + repo))
	return filepath.Join(s.secretsDir(), hex.EncodeToString(sum[:])[:16]+".json")
}

// Secrets returns the stored secret values of a repository, by name: empty
// without a file.
func (s *Service) Secrets(project domain.ProjectID, repo string) (map[string]string, error) {
	s.secretsMu.Lock()
	defer s.secretsMu.Unlock()
	return s.readSecrets(project, repo)
}

// storedSecretNames returns the names of a repository's stored secrets,
// sorted.
func (s *Service) storedSecretNames(project domain.ProjectID, repo string) ([]string, error) {
	values, err := s.Secrets(project, repo)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	slices.Sort(names)
	return names, nil
}

// missingSecrets returns the secret names of the settings without a stored
// value, in the settings' order.
func (s *Service) missingSecrets(settings domain.LadoDBChangeSettings) ([]string, error) {
	if len(settings.SecretNames) == 0 {
		return nil, nil
	}
	values, err := s.Secrets(settings.ProjectID, settings.Repo)
	if err != nil {
		return nil, err
	}
	var missing []string
	for _, name := range settings.SecretNames {
		if values[name] == "" {
			missing = append(missing, name)
		}
	}
	return missing, nil
}

// readSecrets reads a repository's secrets file. The caller holds secretsMu.
func (s *Service) readSecrets(project domain.ProjectID, repo string) (map[string]string, error) {
	path := s.secretsPath(project, repo)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the database change secrets: %w", err)
	}
	var file secretsFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("the database change secrets file %s is not valid JSON", path)
	}
	if file.ProjectID != project || file.Repo != repo {
		return nil, fmt.Errorf("the database change secrets file %s belongs to another repository", path)
	}
	if file.Secrets == nil {
		file.Secrets = map[string]string{}
	}
	return file.Secrets, nil
}

// secretsChange is what a settings change does to the stored secrets.
type secretsChange struct {
	// next is the stored values after the change.
	next map[string]string
	// set and removed are the names whose values the change sets or
	// removes, sorted: removed includes the names the settings no longer
	// list.
	set, removed []string
}

// changed reports whether the change touches the file.
func (c secretsChange) changed() bool { return len(c.set) > 0 || len(c.removed) > 0 }

// planSecrets checks a secrets update against the names the settings will
// list and works out the stored values after it: a value is set only for a
// listed name, and the values of names no longer listed go. It returns the
// problems of the update, nil when there is none.
func planSecrets(current map[string]string, names []string, update domain.LadoDBChangeSecretsUpdate) (secretsChange, []domain.LadoDBChangeProblem) {
	var problems []domain.LadoDBChangeProblem
	next := map[string]string{}
	for name, value := range current {
		next[name] = value
	}
	change := secretsChange{}
	setNames := make([]string, 0, len(update.Set))
	for name := range update.Set {
		setNames = append(setNames, name)
	}
	slices.Sort(setNames)
	for _, name := range setNames {
		value := update.Set[name]
		field := "secrets.set." + name
		switch {
		case !slices.Contains(names, name):
			problems = append(problems, domain.LadoDBChangeProblem{Field: field, Reason: domain.LadoDBChangeProblemInvalid})
		case value == "":
			problems = append(problems, domain.LadoDBChangeProblem{Field: field, Reason: domain.LadoDBChangeProblemRequired})
		case len(value) > SecretValueMax:
			problems = append(problems, domain.LadoDBChangeProblem{Field: field, Reason: domain.LadoDBChangeProblemTooLong})
		case !utf8.ValidString(value) || strings.ContainsAny(value, "\x00\r\n"):
			problems = append(problems, domain.LadoDBChangeProblem{Field: field, Reason: domain.LadoDBChangeProblemControl})
		case slices.Contains(update.Remove, name):
			problems = append(problems, domain.LadoDBChangeProblem{Field: field, Reason: domain.LadoDBChangeProblemDuplicate})
		default:
			if current[name] != value {
				change.set = append(change.set, name)
			}
			next[name] = value
		}
	}
	for _, name := range update.Remove {
		if _, ok := next[name]; ok && !slices.Contains(change.removed, name) {
			delete(next, name)
			change.removed = append(change.removed, name)
		}
	}
	for name := range next {
		if !slices.Contains(names, name) {
			delete(next, name)
			change.removed = append(change.removed, name)
		}
	}
	slices.Sort(change.removed)
	change.next = next
	return change, problems
}

// writeSecrets replaces a repository's secrets file atomically, or removes
// it when no value is left. The caller holds secretsMu.
func (s *Service) writeSecrets(project domain.ProjectID, repo string, values map[string]string) error {
	path := s.secretsPath(project, repo)
	if len(values) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove the database change secrets file: %w", err)
		}
		return nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create the LADO database secrets directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // G302: a directory needs its execute bit; 0700 keeps it to the user.
		return fmt.Errorf("protect the LADO database secrets directory: %w", err)
	}
	data, err := json.MarshalIndent(secretsFile{Version: 1, ProjectID: project, Repo: repo, Secrets: values}, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".db-secrets-*.json")
	if err != nil {
		return fmt.Errorf("write the database change secrets file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("protect the database change secrets file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write the database change secrets file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write the database change secrets file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write the database change secrets file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace the database change secrets file: %w", err)
	}
	return nil
}
