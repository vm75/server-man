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
	"sync"
	"syscall"
	"time"
)

type Command struct {
	Program string
	Args    []string
	Dir     string
	Env     []string
	Timeout time.Duration
	// CancelGrace requests SIGINT first, then kills the process group after this
	// grace period. Zero keeps immediate group cancellation for other backends.
	CancelGrace time.Duration
}

// CaptureError preserves diagnostics separately for CLIs whose empty-result
// exit code is also used for failures. Error and Unwrap preserve existing callers.
type CaptureError struct {
	Err    error
	Stderr string
}

func (e *CaptureError) Error() string {
	if e.Stderr != "" {
		return e.Err.Error() + ": " + e.Stderr
	}
	return e.Err.Error()
}
func (e *CaptureError) Unwrap() error { return e.Err }

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
	var timer *time.Timer
	var timerMu sync.Mutex
	defer func() {
		timerMu.Lock()
		defer timerMu.Unlock()
		if timer != nil {
			timer.Stop()
		}
	}()
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		signal := syscall.SIGKILL
		if command.CancelGrace > 0 {
			signal = syscall.SIGINT
		}
		err := syscall.Kill(-cmd.Process.Pid, signal)
		if err == nil && command.CancelGrace > 0 {
			timerMu.Lock()
			timer = time.AfterFunc(command.CancelGrace, func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
			timerMu.Unlock()
		}
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = 2 * time.Second
	if command.CancelGrace > 0 {
		cmd.WaitDelay += command.CancelGrace
	}
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
		err = &CaptureError{Err: err, Stderr: strings.TrimSpace(message)}
	}
	return text, err
}
