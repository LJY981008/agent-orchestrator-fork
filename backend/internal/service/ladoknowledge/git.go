package ladoknowledge

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// gitTimeout bounds one git call the service makes itself.
const gitTimeout = 10 * time.Second

// Git runs git in a folder and returns what it printed.
type Git interface {
	Run(ctx context.Context, dir string, args ...string) ([]byte, error)
}

// ExecGit runs the system git with prompts off and pathspecs read
// literally, so a source path is never a pattern.
type ExecGit struct{}

// Run runs git -C dir args.
func (ExecGit) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	cmd := aoprocess.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=Never", "GIT_LITERAL_PATHSPECS=1", "GIT_OPTIONAL_LOCKS=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

func yamlUnmarshal(data []byte, v any) error { return yaml.Unmarshal(data, v) }
