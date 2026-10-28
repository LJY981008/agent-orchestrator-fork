//go:build !windows

package ladoverify

import (
	"context"
	"os/exec"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// shellCommand runs command with sh -c.
func shellCommand(ctx context.Context, command string) *exec.Cmd {
	return aoprocess.CommandContext(ctx, "sh", "-c", command)
}
