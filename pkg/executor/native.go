package executor

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// NativeLauncher starts an installed coding agent without changing its
// configuration, environment, working directory, or stdio contract.
type NativeLauncher struct {
	lookup  func(string) (string, error)
	command func(context.Context, string, ...string) *exec.Cmd
}

func NewNativeLauncher() *NativeLauncher {
	return &NativeLauncher{lookup: exec.LookPath, command: commandContext}
}

// Command returns a direct child-process command. The caller owns stdio so
// Bubble Tea can temporarily release and then restore the terminal.
func (l *NativeLauncher) Command(ctx context.Context, agent, model, prompt string) (*exec.Cmd, error) {
	agent = strings.ToLower(strings.TrimSpace(agent))
	if agent != "claude" && agent != "codex" {
		return nil, fmt.Errorf("unsupported native agent %q", agent)
	}
	binary, err := l.lookup(agent)
	if err != nil {
		return nil, fmt.Errorf("%s executable is not installed or not in PATH: %w", agent, err)
	}
	args := nativeArgs(agent, model, prompt)
	return l.command(ctx, binary, args...), nil
}

func nativeArgs(agent, model, prompt string) []string {
	if agent == "codex" {
		args := []string{"exec"}
		if strings.TrimSpace(model) != "" {
			args = append(args, "--model", strings.TrimSpace(model))
		}
		return append(args, prompt)
	}
	args := make([]string, 0, 3)
	if strings.TrimSpace(model) != "" {
		args = append(args, "--model", strings.TrimSpace(model))
	}
	return append(args, prompt)
}
