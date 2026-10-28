//go:build windows

package ladoverify

import (
	"context"
	"os/exec"
	"syscall"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// shellCommand runs command with cmd /d /s /c on the command line as it is
// written (S1 of #15). Go quotes each argument the way programs that parse
// their command line like the C runtime expect, which cmd does not: the
// pattern of go test -run "A|B" would reach go with its quotes and match no
// test. /s makes cmd take everything between the first and the last quote
// as it is, and /d skips the AutoRun commands of the registry, such as
// conda's or clink's. adapters/systemexec and previewserver start batch files
// the same way.
func shellCommand(ctx context.Context, command string) *exec.Cmd {
	cmd := aoprocess.CommandContext(ctx, "cmd")
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CmdLine = `cmd /d /s /c "` + command + `"`
	return cmd
}
