package admin

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"server-man/internal/config"
	"server-man/internal/core"
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

func TestAuthoritativeSharesRejectUnvalidatedDefinitions(t *testing.T) {
	for _, arguments := range []string{
		`{"protocol":"samba","definitions":"[{\"name\":\"global\",\"path\":\"/tmp\",\"users\":[\"alice\"]}]"}`,
		`{"protocol":"nfs","definitions":"[{\"name\":\"data\",\"path\":\"/tmp\",\"clients\":[\"*\"],\"command\":\"bad\"}]"}`,
	} {
		var output bytes.Buffer
		request := `{"operation":"shares_definitions_inspect","arguments":` + arguments + `}`
		if err := Serve(context.Background(), config.Config{}, noCommands{t}, strings.NewReader(request), &output); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(output.String(), `"error"`) {
			t.Fatal("invalid authoritative definitions accepted", output.String())
		}
	}
}

func TestPasswordHelperRejectsCallerSelectedAccount(t *testing.T) {
	for _, args := range []string{
		`{"password":"synthetic","username":"another","confirm":true}`,
		`{"password":"","confirm":true}`,
		`{"password":" \t ","confirm":true}`,
		`{"password":"synthetic","confirm":false}`,
	} {
		var output bytes.Buffer
		request := `{"operation":"shares_samba_password","arguments":` + args + `}`
		if err := ServeForAccount(context.Background(), config.Config{}, noCommands{t}, strings.NewReader(request), &output, "workload"); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(output.String(), `"error"`) {
			t.Fatal("unsafe password request accepted")
		}
	}
}

// Simulate a native program echoing stdin on both success and failure. The
// helper must expose only the selected account and a fixed diagnostic.
type passwordNativeFixture struct {
	t        *testing.T
	password string
	fail     bool
	calls    int
}

func (f *passwordNativeFixture) Capture(context.Context, core.Command) (string, error) {
	f.t.Fatal("unexpected capture")
	return "", nil
}
func (f *passwordNativeFixture) Run(_ context.Context, c core.Command, out io.Writer) error {
	f.calls++
	if filepath.Base(c.Program) != "smbpasswd" || strings.Join(c.Args, " ") != "-s -a workload" {
		f.t.Fatal("password helper targeted a different account")
	}
	input, err := io.ReadAll(c.Stdin)
	if err != nil || string(input) != f.password+"\n"+f.password+"\n" {
		f.t.Fatal("incorrect password stdin")
	}
	io.WriteString(out, f.password)
	if f.fail {
		return errors.New(f.password)
	}
	return nil
}
func TestPasswordHelperBindsTrustedCallerAndDiscardsSecrets(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "smbpasswd"), []byte("fixture executable"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	for _, fail := range []bool{false, true} {
		password := t.Name() + "-synthetic"
		native := &passwordNativeFixture{t: t, password: password, fail: fail}
		var out bytes.Buffer
		request := `{"operation":"shares_samba_password","arguments":{"password":"` + password + `","confirm":true}}`
		if err := ServeForAccount(context.Background(), config.Config{}, native, strings.NewReader(request), &out, "workload"); err != nil {
			t.Fatal(err)
		}
		if native.calls != 1 || strings.Contains(out.String(), password) {
			t.Fatal("incorrect helper execution or retained secret")
		}
		if fail != strings.Contains(out.String(), `"error"`) {
			t.Fatal("incorrect helper response state")
		}
		if !fail && !strings.Contains(out.String(), `"username":"workload"`) {
			t.Fatal("helper did not report caller account")
		}
	}
}
