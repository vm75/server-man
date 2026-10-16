package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type Command struct {
	Program string
	Args    []string
	Dir     string
	Env     []string
	Timeout time.Duration
}

// Executor permits integration tests to record commands without changing a host.
type Executor interface {
	Run(context.Context, Command, io.Writer) error
	Capture(context.Context, Command) (string, error)
}

type Runner struct{}

func (Runner) Run(ctx context.Context, command Command, output io.Writer) error {
	return run(ctx, command, output, output)
}

func run(ctx context.Context, command Command, stdout, stderr io.Writer) error {
	timeout := command.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, command.Program, command.Args...)
	cmd.Dir = command.Dir
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C", "SYSTEMD_PAGER=cat", "PAGER=cat", "TERM=dumb")
	cmd.Env = append(cmd.Env, command.Env...)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Arguments can contain private configuration; never include them here.
		return fmt.Errorf("%s: %w", filepath.Base(command.Program), err)
	}
	return nil
}

func (r Runner) Capture(ctx context.Context, command Command) (string, error) {
	if command.Timeout <= 0 {
		command.Timeout = 15 * time.Second
	}
	output := NewTail(1 << 20)
	stderr := NewTail(16 << 10)
	err := run(ctx, command, output, stderr)
	text, truncated := output.Snapshot()
	if truncated {
		return "", errors.New("command output exceeds the 1 MiB capture limit")
	}
	if err != nil {
		message, _ := stderr.Snapshot()
		if message = strings.TrimSpace(message); message != "" {
			err = fmt.Errorf("%w: %s", err, message)
		}
	}
	return text, err
}
