package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func helperCommand(t *testing.T, mode string) Command {
	t.Helper()
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Command{Program: program, Args: []string{"-test.run=^TestRunnerHelperProcess$"}, Env: []string{"SERVERMAN_TEST_HELPER=" + mode, "GORACE=atexit_sleep_ms=0"}}
}

func TestRunnerHelperProcess(t *testing.T) {
	switch os.Getenv("SERVERMAN_TEST_HELPER") {
	case "output":
		fmt.Fprint(os.Stdout, `{"result":"ok"}`)
		fmt.Fprint(os.Stderr, "diagnostic warning")
		os.Exit(0)
	case "fail":
		fmt.Fprint(os.Stderr, "backend unavailable")
		os.Exit(7)
	case "wait":
		time.Sleep(time.Minute)
		os.Exit(0)
	case "overflow":
		fmt.Fprint(os.Stdout, strings.Repeat("x", (1<<20)+1))
		os.Exit(0)
	}
}

func TestCaptureSeparatesDiagnosticsAndPreservesExitStatus(t *testing.T) {
	output, err := (Runner{}).Capture(context.Background(), helperCommand(t, "output"))
	if err != nil || output != `{"result":"ok"}` {
		t.Fatalf("output=%q, error=%v", output, err)
	}
	_, err = (Runner{}).Capture(context.Background(), helperCommand(t, "fail"))
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 7 || !strings.Contains(err.Error(), "backend unavailable") {
		t.Fatalf("lost exit status or diagnostics: %v", err)
	}
}

func TestRunnerTimeoutAndCaptureLimit(t *testing.T) {
	command := helperCommand(t, "wait")
	command.Timeout = 100 * time.Millisecond
	start := time.Now()
	err := (Runner{}).Run(context.Background(), command, io.Discard)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 3*time.Second {
		t.Fatalf("timeout not preserved: %v", err)
	}
	if _, err := (Runner{}).Capture(context.Background(), helperCommand(t, "overflow")); err == nil {
		t.Fatal("oversized capture accepted")
	}
}
