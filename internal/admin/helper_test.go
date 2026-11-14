package admin

import (
	"bytes"
	"context"
	"errors"
	"io"
	"serverman/internal/config"
	"serverman/internal/core"
	"strings"
	"testing"
	"time"
)

type noCommands struct{ t *testing.T }

func (n noCommands) Run(context.Context, core.Command, io.Writer) error {
	n.t.Fatal("unexpected native command")
	return nil
}
func (n noCommands) Capture(context.Context, core.Command) (string, error) {
	n.t.Fatal("unexpected native command")
	return "", nil
}
func TestRejectRequests(t *testing.T) {
	for _, input := range []string{
		`{}`, `{"operation":"compose_up"}`, `{"operation":"services_start","extra":true}`,
		`{"operation":"services_start"} {}`, strings.Repeat("x", (1<<20)+1),
	} {
		t.Run(input[:min(len(input), 50)], func(t *testing.T) {
			var out bytes.Buffer
			if err := Serve(context.Background(), config.Config{}, noCommands{t}, strings.NewReader(input), &out); err == nil {
				t.Fatal("accepted invalid request")
			}
		})
	}
}
func TestValidateBeforeExecution(t *testing.T) {
	for _, args := range []string{`{"unit":"x.service","confirm":false}`, `{"unit":"x.service","confirm":true,"command":"sh"}`, `{"unit":"../bad.service","confirm":true}`} {
		var out bytes.Buffer
		input := `{"operation":"services_stop","arguments":` + args + `}`
		if err := Serve(context.Background(), config.Config{}, noCommands{t}, strings.NewReader(input), &out); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), `"error"`) {
			t.Fatal(out.String())
		}
	}
}

func TestStalledRequestReleasesOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, config.Config{}, noCommands{t}, reader, io.Discard) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("stalled input ignored cancellation")
	}
}
