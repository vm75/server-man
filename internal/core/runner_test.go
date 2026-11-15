package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func helperCommand(t *testing.T, mode string) Command {
	t.Helper()
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Command{Program: program, Args: []string{"-test.run=^TestRunnerHelperProcess$"}, Env: []string{"SERVER_MAN_TEST_HELPER=" + mode, "GORACE=atexit_sleep_ms=0"}}
}

func TestRunnerHelperProcess(t *testing.T) {
	switch os.Getenv("SERVER_MAN_TEST_HELPER") {
	case "binary-stream":
		block := bytes.Repeat([]byte{0, 1, 2, 255}, 32<<10)
		for range 32 {
			_, _ = os.Stdout.Write(block)
		}
		_, _ = io.WriteString(os.Stderr, "diagnostic-only")
		os.Exit(0)
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
	case "empty-exit":
		os.Exit(1)
	case "interrupt":
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, os.Interrupt)
		fmt.Fprintln(os.Stdout, "ready")
		<-signals
		fmt.Fprintln(os.Stdout, "interrupted")
		os.Exit(0)
	case "ignore-interrupt":
		signal.Ignore(os.Interrupt)
		fmt.Fprintln(os.Stdout, "ready")
		time.Sleep(time.Minute)
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
	var captured *CaptureError
	if !errors.As(err, &captured) || captured.Stderr != "backend unavailable" {
		t.Fatal("lost separate diagnostics", err)
	}
	_, err = (Runner{}).Capture(context.Background(), helperCommand(t, "empty-exit"))
	if !errors.As(err, &captured) || captured.Stderr != "" {
		t.Fatal("empty failure diagnostics were not preserved", err)
	}
}

type readyOutput struct {
	tail  *Tail
	ready chan struct{}
	once  sync.Once
}

func (w *readyOutput) Write(p []byte) (int, error) {
	n, err := w.tail.Write(p)
	if strings.Contains(string(p), "ready") {
		w.once.Do(func() { close(w.ready) })
	}
	return n, err
}
func TestRunnerInterruptsBeforeForcedCancellation(t *testing.T) {
	for _, mode := range []string{"interrupt", "ignore-interrupt"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			command := helperCommand(t, mode)
			command.CancelGrace = 150 * time.Millisecond
			out := &readyOutput{tail: NewTail(1024), ready: make(chan struct{})}
			done := make(chan error, 1)
			go func() { done <- (Runner{}).Run(ctx, command, out) }()
			select {
			case <-out.ready:
			case err := <-done:
				t.Fatal("helper exited before readiness", err)
			case <-time.After(5 * time.Second):
				t.Fatal("helper not ready")
			}
			start := time.Now()
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case <-time.After(4 * time.Second):
				t.Fatal("cancellation did not finish")
			}
			text, _ := out.tail.Snapshot()
			if mode == "interrupt" && !strings.Contains(text, "interrupted") {
				t.Fatal("native interrupt handler did not run", text)
			}
			if mode == "ignore-interrupt" && (time.Since(start) < 100*time.Millisecond || time.Since(start) > 2*time.Second) {
				t.Fatal("forced group cleanup missed its grace period", time.Since(start))
			}
		})
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

func TestStreamKeepsLargeBinaryDataSeparateFromDiagnostics(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stream.bin")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	hash := sha256.New()
	diagnostic := NewTail(1024)
	if err = (Runner{}).Stream(context.Background(), helperCommand(t, "binary-stream"), io.MultiWriter(f, hash), diagnostic); err != nil {
		t.Fatal(err)
	}
	info, err := f.Stat()
	if err != nil || info.Size() != 4<<20 {
		t.Fatal(info, err)
	}
	expected := sha256.Sum256(bytes.Repeat([]byte{0, 1, 2, 255}, 1<<20))
	if !bytes.Equal(hash.Sum(nil), expected[:]) {
		t.Fatal("binary stream was truncated or contaminated")
	}
	text, truncated := diagnostic.Snapshot()
	if truncated || text != "diagnostic-only" {
		t.Fatal(text, truncated)
	}
}
