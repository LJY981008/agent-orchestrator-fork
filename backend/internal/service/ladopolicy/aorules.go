package ladopolicy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// AORuleKind names an AO project instruction setting (domain.ProjectConfig).
type AORuleKind string

// AO instruction kinds, in the order ReadAORules returns them.
const (
	// AORuleWorkerInstructions is the agentRules text for workers.
	AORuleWorkerInstructions AORuleKind = "worker_instructions"
	// AORuleWorkerInstructionsFile is the agentRulesFile, whose text AO adds
	// to the workers' instructions.
	AORuleWorkerInstructionsFile AORuleKind = "worker_instructions_file"
	// AORuleOrchestratorInstructions is the orchestratorRules text.
	AORuleOrchestratorInstructions AORuleKind = "orchestrator_instructions"
)

// maxAORulesFileBytes caps the agentRulesFile LADO reads.
const maxAORulesFileBytes = 4 << 20

// AORule is one AO project instruction, which LADO shows as a read-only
// Project entry (docs/LJY-PRD/HARNESS.md §2). AO sends the text itself, so
// LADO keeps only its hash and length.
type AORule struct {
	Kind AORuleKind
	// Path is the configured agentRulesFile, empty for the other kinds.
	Path string
	// SHA256 is the hex sha256 of the trimmed text, empty when the file was
	// not read.
	SHA256 string
	// Chars is the length of the trimmed text in runes.
	Chars int
	// ReadError says why the file was not read, empty when it was.
	ReadError string
}

// ReadAORules reads the project's AO instructions. A setting that is empty
// or only whitespace gives no entry, and an agentRulesFile that cannot be
// read, or is not read because ctx is done, gives one with ReadError.
func ReadAORules(ctx context.Context, project domain.ProjectRecord) []AORule {
	cfg := project.Config
	rules := make([]AORule, 0, 3)
	if text := strings.TrimSpace(cfg.AgentRules); text != "" {
		rules = append(rules, textAORule(AORuleWorkerInstructions, text))
	}
	if rel := strings.TrimSpace(cfg.AgentRulesFile); rel != "" {
		rules = append(rules, fileAORule(ctx, project.Path, rel))
	}
	if text := strings.TrimSpace(cfg.OrchestratorRules); text != "" {
		rules = append(rules, textAORule(AORuleOrchestratorInstructions, text))
	}
	return rules
}

// textAORule describes trimmed instruction text.
func textAORule(kind AORuleKind, text string) AORule {
	sum := sha256.Sum256([]byte(text))
	return AORule{Kind: kind, SHA256: hex.EncodeToString(sum[:]), Chars: utf8.RuneCountInString(text)}
}

// fileAORule describes the agentRulesFile rel of the project at
// projectPath.
func fileAORule(ctx context.Context, projectPath, rel string) AORule {
	data, err := readProjectFile(ctx, projectPath, rel)
	if err != nil {
		return AORule{Kind: AORuleWorkerInstructionsFile, Path: rel, ReadError: err.Error()}
	}
	rule := textAORule(AORuleWorkerInstructionsFile, strings.TrimSpace(string(data)))
	rule.Path = rel
	return rule
}

// readProjectFile reads the file rel names under projectPath, the file AO
// reads when it starts a worker, up to maxAORulesFileBytes. Its errors leave
// out where the project lives: they are shown next to rel and go into rule
// snapshots.
func readProjectFile(ctx context.Context, projectPath, rel string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := projectRelativeFile(projectPath, rel)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path) //nolint:gosec // path is under the project, checked by projectRelativeFile
	if err != nil {
		return nil, fileError(err)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxAORulesFileBytes+1))
	if err != nil {
		return nil, fileError(err)
	}
	if len(data) > maxAORulesFileBytes {
		return nil, errors.New("larger than 4 MiB")
	}
	return data, nil
}

// fileError drops the path from a file system error.
func fileError(err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return errors.New("file not found")
	}
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err
	}
	return err
}

// projectRelativeFile is a copy of the unexported projectRelativeFile in
// session_manager/prompt.go, which buildProjectRules uses to read
// agentRulesFile: LADO refuses the paths AO refuses and reads the file AO
// reads.
func projectRelativeFile(projectPath, rel string) (string, error) {
	if strings.TrimSpace(projectPath) == "" {
		return "", fmt.Errorf("project path is required")
	}
	trimmed := strings.TrimSpace(rel)
	if filepath.IsAbs(trimmed) || strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, `\`) {
		return "", fmt.Errorf("path must be repo-relative and must not escape the project root")
	}
	clean := filepath.Clean(trimmed)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path must be repo-relative and must not escape the project root")
	}
	for _, seg := range strings.Split(filepath.ToSlash(clean), "/") {
		if seg == ".." {
			return "", fmt.Errorf("path must be repo-relative and must not escape the project root")
		}
	}
	return filepath.Join(projectPath, clean), nil
}
