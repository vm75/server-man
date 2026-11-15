package core

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSchemaRejectsInvalidArguments(t *testing.T) {
	minimum, maximum := 1.0, 10.0
	fields := map[string]Field{
		"name":   StringField("Resource name", true, 10),
		"action": EnumField("Action", true, "start", "stop"),
		"force":  {Type: "boolean"},
		"count":  {Type: "integer", Minimum: &minimum, Maximum: &maximum},
	}
	for _, tc := range []struct {
		body  string
		valid bool
	}{
		{`{"name":"demo","action":"start"}`, true},
		{`{"name":"demo","action":"stop","force":false,"count":2}`, true},
		{`null`, false}, {`[]`, false}, {`{} {}`, false},
		{`{"name":"","action":"start"}`, false},
		{`{"name":"abcdefghijk","action":"start"}`, false},
		{`{"name":"demo","action":"destroy"}`, false},
		{`{"name":"demo","action":"stop","shell":"rm"}`, false},
		{`{"name":"demo","action":"stop","force":"true"}`, false},
		{`{"name":"demo","action":"stop","count":1.5}`, false},
		{`{"name":"demo","action":"stop","count":11}`, false},
	} {
		_, err := validate(json.RawMessage(tc.body), fields)
		if (err == nil) != tc.valid {
			t.Errorf("%s: valid=%v, error=%v", tc.body, tc.valid, err)
		}
	}
}

func TestCancellationPreservesRollbackDiagnostics(t *testing.T) {
	jobs, err := NewJobs(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer jobs.Close()
	started := make(chan struct{})
	job, err := jobs.Start("test_rollback", "test", func(ctx context.Context, out io.Writer) (any, error) {
		close(started)
		<-ctx.Done()
		return nil, errors.New("rollback incomplete: inspect configuration")
	})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if err := jobs.Cancel(job.ID); err != nil {
		t.Fatal(err)
	}
	finished := awaitJob(t, jobs, job.ID)
	if finished.State != "cancelled" || !strings.Contains(finished.Error, "rollback incomplete") {
		t.Fatal(finished)
	}
}

func TestRegistryWritePolicyAndDestructiveAcknowledgement(t *testing.T) {
	jobs, err := NewJobs(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer jobs.Close()
	var calls atomic.Int32
	tool := Tool{Name: "remove_resource", Destructive: true, Fields: map[string]Field{"name": StringField("Name", true, 20)}, Run: func(ctx context.Context, input Input, out io.Writer) (any, error) { calls.Add(1); return "done", nil }}
	registry := NewRegistry(false, jobs)
	if err := registry.AddModule(Module{ID: "demo", Available: true}, tool); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"name":"demo"}`, `{"name":"demo","confirm":false}`, `{"name":"demo","confirm":true}`, `{"name":"demo","confirm":true,"shell":"x"}`} {
		if _, err := registry.Invoke(context.Background(), tool.Name, json.RawMessage(body), "test"); err == nil {
			t.Fatalf("denied write accepted: %s", body)
		}
	}
	if calls.Load() != 0 || len(jobs.List()) != 0 {
		t.Fatal("denied request launched a job")
	}
	registry.AllowWrites = true
	result, err := registry.Invoke(context.Background(), tool.Name, json.RawMessage(`{"name":"demo","confirm":true}`), "test")
	if err != nil {
		t.Fatal(err)
	}
	job := result.(map[string]any)["job"].(Job)
	finished := awaitJob(t, jobs, job.ID)
	if finished.State != "succeeded" || calls.Load() != 1 {
		t.Fatalf("job=%+v calls=%d", finished, calls.Load())
	}
}

func TestJobSerializationCancellationOutputAndAudit(t *testing.T) {
	root := t.TempDir()
	jobs, err := NewJobs(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer jobs.Close()
	started := make(chan struct{})
	job, err := jobs.Start("test_wait", "test", func(ctx context.Context, out io.Writer) (any, error) {
		_, _ = io.WriteString(out, strings.Repeat("x", 140<<10)+"last line")
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if _, err := jobs.Start("other", "test", func(context.Context, io.Writer) (any, error) { t.Error("overlapping job ran"); return nil, nil }); err == nil {
		t.Fatal("overlapping job accepted")
	}
	if err := jobs.Cancel(job.ID); err != nil {
		t.Fatal(err)
	}
	finished := awaitJob(t, jobs, job.ID)
	if finished.State != "cancelled" || !finished.Truncated || len(finished.Output) != 128<<10 || !strings.HasSuffix(finished.Output, "last line") {
		t.Fatalf("incorrect terminal job: state=%s truncated=%v bytes=%d", finished.State, finished.Truncated, len(finished.Output))
	}
	audit, _ := os.ReadFile(filepath.Join(root, "audit.jsonl"))
	if strings.Contains(string(audit), "last line") || !strings.Contains(string(audit), "cancel_requested") || !strings.Contains(string(audit), "cancelled") {
		t.Fatalf("unexpected audit metadata: %s", audit)
	}
	if err := jobs.Cancel(job.ID); err == nil {
		t.Fatal("terminal job cancelled again")
	}
}

func TestFailedAndPanickingJobsReachTerminalState(t *testing.T) {
	jobs, err := NewJobs(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer jobs.Close()
	for _, run := range []func(context.Context, io.Writer) (any, error){
		func(context.Context, io.Writer) (any, error) { return nil, errors.New("failure") },
		func(context.Context, io.Writer) (any, error) { panic("do not expose this value") },
	} {
		job, err := jobs.Start("failure", "test", run)
		if err != nil {
			t.Fatal(err)
		}
		if done := awaitJob(t, jobs, job.ID); done.State != "failed" || strings.Contains(done.Error, "do not expose") {
			t.Fatalf("incorrect failure: %+v", done)
		}
	}
}

func TestTailExactLimitAndReset(t *testing.T) {
	tail := NewTail(4)
	_, _ = tail.Write([]byte("1234"))
	if text, truncated := tail.Snapshot(); text != "1234" || truncated {
		t.Fatal("exact limit was truncated")
	}
	_, _ = tail.Write([]byte("56"))
	if text, truncated := tail.Snapshot(); text != "3456" || !truncated {
		t.Fatal("tail was not retained")
	}
}

func TestReadConcurrencyIsBoundedAndCancellationReleasesWaiters(t *testing.T) {
	registry := NewRegistry(false, nil)
	var active, peak atomic.Int32
	if err := registry.AddModule(Module{ID: "demo", Available: true}, Tool{Name: "block", ReadOnly: true, Run: func(ctx context.Context, input Input, out io.Writer) (any, error) {
		current := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); current > old; old = peak.Load() {
			if peak.CompareAndSwap(old, current) {
				break
			}
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	var wait sync.WaitGroup
	for range 8 {
		wait.Go(func() { _, _ = registry.Invoke(ctx, "block", nil, "test") })
	}
	wait.Wait()
	if peak.Load() > 4 || peak.Load() == 0 || active.Load() != 0 {
		t.Fatalf("peak=%d active=%d", peak.Load(), active.Load())
	}
}

func TestJobHistoryIsBoundedAndAuditFailurePreventsExecution(t *testing.T) {
	root := t.TempDir()
	jobs, err := NewJobs(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer jobs.Close()
	var first string
	for range 65 {
		job, err := jobs.Start("noop", "test", func(context.Context, io.Writer) (any, error) { return nil, nil })
		if err != nil {
			t.Fatal(err)
		}
		if first == "" {
			first = job.ID
		}
		awaitJob(t, jobs, job.ID)
	}
	if len(jobs.List()) != 64 {
		t.Fatal("job retention is not bounded")
	}
	if _, err := jobs.Get(first); err == nil {
		t.Fatal("oldest job was not evicted")
	}
	path := filepath.Join(root, "audit.jsonl")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.Start("must_not_run", "test", func(context.Context, io.Writer) (any, error) { t.Error("unaudited operation ran"); return nil, nil }); err == nil {
		t.Fatal("unaudited operation accepted")
	}
}

func TestManagersSharingStateSerializeMutations(t *testing.T) {
	root := t.TempDir()
	first, err := NewJobs(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := NewJobs(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	job, err := first.Start("hold", "test", func(ctx context.Context, out io.Writer) (any, error) { <-ctx.Done(); return nil, ctx.Err() })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.Start("must_not_run", "test", func(context.Context, io.Writer) (any, error) {
		t.Error("concurrent manager ran a mutation")
		return nil, nil
	}); err == nil {
		t.Fatal("cross-manager write lock was not enforced")
	}
	if err := first.Cancel(job.ID); err != nil {
		t.Fatal(err)
	}
	awaitJob(t, first, job.ID)
	job, err = second.Start("after_release", "test", func(context.Context, io.Writer) (any, error) { return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	if finished := awaitJob(t, second, job.ID); finished.State != "succeeded" {
		t.Fatal("lock was not released")
	}
}

func awaitJob(t *testing.T, jobs *Jobs, id string) Job {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		job, err := jobs.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if job.State != "running" {
			return job
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("job did not finish")
		}
	}
}

func TestTrustedJobTimeoutsRemainBoundedAndCannotBeSetByRequests(t *testing.T) {
	jobs, err := NewJobs(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer jobs.Close()
	registry := NewRegistry(true, jobs)
	observed := make(chan time.Duration, 1)
	if err = registry.AddModule(Module{ID: "backup", Available: true}, Tool{Name: "backup_test", JobTimeout: 2 * time.Hour, Run: func(ctx context.Context, _ Input, _ io.Writer) (any, error) {
		deadline, ok := ctx.Deadline()
		if !ok {
			return nil, errors.New("missing deadline")
		}
		observed <- time.Until(deadline)
		return nil, nil
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err = registry.Invoke(context.Background(), "backup_test", json.RawMessage(`{"timeout":86400}`), "test"); err == nil {
		t.Fatal("request could set execution deadline")
	}
	result, err := registry.Invoke(context.Background(), "backup_test", json.RawMessage(`{}`), "test")
	if err != nil {
		t.Fatal(err)
	}
	job := result.(map[string]any)["job"].(Job)
	if completed := awaitJob(t, jobs, job.ID); completed.State != "succeeded" {
		t.Fatal(completed)
	}
	remaining := <-observed
	if remaining < 119*time.Minute || remaining > 2*time.Hour {
		t.Fatal("trusted timeout did not reach job context", remaining)
	}
	for _, tool := range []Tool{{Name: "negative", JobTimeout: -time.Second}, {Name: "unbounded", JobTimeout: 25 * time.Hour}, {Name: "read", ReadOnly: true, JobTimeout: time.Hour}, {Name: "immediate", Immediate: true, JobTimeout: time.Hour}} {
		tool.Run = func(context.Context, Input, io.Writer) (any, error) { return nil, nil }
		if err = NewRegistry(true, jobs).AddModule(Module{ID: "invalid", Available: true}, tool); err == nil {
			t.Fatal("invalid timeout policy accepted", tool.Name)
		}
	}
	short, err := jobs.StartWithTimeout("short", "test", 10*time.Millisecond, func(ctx context.Context, _ io.Writer) (any, error) { <-ctx.Done(); return nil, ctx.Err() })
	if err != nil {
		t.Fatal(err)
	}
	if completed := awaitJob(t, jobs, short.ID); completed.State != "cancelled" || !strings.Contains(completed.Error, "deadline") {
		t.Fatal(completed)
	}
}
