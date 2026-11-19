package shares

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"server-man/internal/config"
	"server-man/internal/core"
)

type passwordExecutor struct {
	t                 *testing.T
	account, password string
	calls             int
	fail              bool
}

func (r *passwordExecutor) Capture(context.Context, core.Command) (string, error) {
	r.t.Fatal("unexpected capture")
	return "", nil
}
func (r *passwordExecutor) Run(_ context.Context, c core.Command, out io.Writer) error {
	r.calls++
	if c.Program != "/fixture/smbpasswd" || strings.Join(c.Args, " ") != "-s -a "+r.account || c.Timeout != 20*time.Second {
		r.t.Fatal("unexpected password command")
	}
	input, err := io.ReadAll(c.Stdin)
	if err != nil || string(input) != r.password+"\n"+r.password+"\n" {
		r.t.Fatal("incorrect password input")
	}
	io.WriteString(out, r.password) // Even a native command echo must be discarded.
	if r.fail {
		return errors.New(r.password)
	}
	return nil
}
func TestSambaPasswordValidationAndPrivacy(t *testing.T) {
	password := t.Name() + "-synthetic"
	for _, fail := range []bool{false, true} {
		runner := &passwordExecutor{t: t, account: "workload", password: password, fail: fail}
		b := NewForAccount(config.Config{}, runner, "workload")
		b.programs["smbpasswd"] = "/fixture/smbpasswd"
		registry := core.NewRegistry(true, nil)
		if err := b.Register(registry); err != nil {
			t.Fatal(err)
		}
		for _, value := range []string{"", "   ", "a\nb", "a\rb", "a\x00b", strings.Repeat("a", 257)} {
			raw, _ := json.Marshal(core.Input{"password": value, "confirm": true})
			if _, err := registry.Execute(context.Background(), "shares_samba_password", raw, io.Discard); err == nil {
				t.Fatal("invalid password accepted")
			}
		}
		for _, input := range []core.Input{{"password": password, "confirm": false}, {"password": password, "confirm": true, "username": "another"}} {
			raw, _ := json.Marshal(input)
			if _, err := registry.Execute(context.Background(), "shares_samba_password", raw, io.Discard); err == nil {
				t.Fatal("invalid request accepted")
			}
		}
		if runner.calls != 0 {
			t.Fatal("invalid request ran a command")
		}
		raw, _ := json.Marshal(core.Input{"password": password, "confirm": true})
		var output bytes.Buffer
		result, err := registry.Execute(context.Background(), "shares_samba_password", raw, &output)
		if (err != nil) != fail || runner.calls != 1 {
			t.Fatal("unexpected command outcome")
		}
		encoded, _ := json.Marshal(result)
		if output.Len() != 0 || strings.Contains(string(encoded), password) || err != nil && strings.Contains(err.Error(), password) {
			t.Fatal("secret retained")
		}
	}
}
func TestSambaPasswordJobDoesNotRetainSecret(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	jobs, err := core.NewJobs(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer jobs.Close()
	password := t.Name() + "-synthetic"
	runner := &passwordExecutor{t: t, account: "workload", password: password, fail: true}
	b := NewForAccount(config.Config{}, runner, "workload")
	b.programs["smbpasswd"] = "/fixture/smbpasswd"
	registry := core.NewRegistry(false, jobs)
	if err := b.Register(registry); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(core.Input{"password": password, "confirm": true})
	if _, err := registry.Invoke(ctx, "shares_samba_password", raw, "test"); err == nil {
		t.Fatal("disabled write accepted")
	}
	registry.AllowWrites = true
	result, err := registry.Invoke(ctx, "shares_samba_password", raw, "test")
	if err != nil {
		t.Fatal(err)
	}
	job := result.(map[string]any)["job"].(core.Job)
	deadline := time.Now().Add(3 * time.Second)
	for {
		job, err = jobs.Get(job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if job.State != "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("job did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	audit, err := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(job)
	if job.State != "failed" || strings.Contains(string(encoded), password) || strings.Contains(string(audit), password) {
		t.Fatal("job/audit retained secret or wrong state")
	}
}
