package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"time"
)

type Job struct {
	ID         string     `json:"id"`
	Tool       string     `json:"tool"`
	State      string     `json:"state"`
	CreatedAt  time.Time  `json:"createdAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	Output     string     `json:"output"`
	Truncated  bool       `json:"truncated"`
	Error      string     `json:"error,omitempty"`
	Result     any        `json:"result,omitempty"`
}

type runningJob struct {
	job    Job
	output *Tail
	cancel context.CancelFunc
	done   chan struct{}
	source string
}

type Jobs struct {
	mu        sync.Mutex
	ctx       context.Context
	jobs      map[string]*runningJob
	order     []string
	active    string
	closed    bool
	auditPath string
}

func NewJobs(ctx context.Context, stateDir string) (*Jobs, error) {
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return nil, fmt.Errorf("create state directory: %w", err)
	}
	path := filepath.Join(stateDir, "audit.jsonl")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, fmt.Errorf("open audit: %w", err)
	}
	info, statErr := file.Stat()
	if statErr != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		_ = file.Close()
		return nil, errors.New("audit log must be a regular file readable only by its owner")
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	return &Jobs{ctx: ctx, jobs: map[string]*runningJob{}, auditPath: path}, nil
}

// Start permits one mutation at a time across all modules. Reads remain independent.
func (j *Jobs) Start(tool, source string, run func(context.Context, io.Writer) (any, error)) (Job, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed || j.ctx.Err() != nil {
		return Job{}, Unavailable("server is shutting down")
	}
	if j.active != "" {
		return Job{}, &Error{Code: "busy", Message: "Another management job is running. Wait for it to finish or cancel it.", Status: 409}
	}
	var idBytes [12]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		return Job{}, err
	}
	id := hex.EncodeToString(idBytes[:])
	ctx, cancel := context.WithTimeout(j.ctx, 30*time.Minute)
	entry := &runningJob{job: Job{ID: id, Tool: tool, State: "running", CreatedAt: time.Now().UTC()}, output: NewTail(128 << 10), cancel: cancel, done: make(chan struct{}), source: source}
	if err := j.audit(entry, "running"); err != nil {
		cancel()
		return Job{}, fmt.Errorf("write audit before execution: %w", err)
	}
	for len(j.order) >= 64 {
		delete(j.jobs, j.order[0])
		j.order = j.order[1:]
	}
	j.jobs[id], j.order, j.active = entry, append(j.order, id), id
	go j.execute(ctx, entry, run)
	return entry.job, nil
}

func (j *Jobs) execute(ctx context.Context, entry *runningJob, run func(context.Context, io.Writer) (any, error)) {
	var result any
	var err error
	func() {
		defer func() {
			if recover() != nil {
				err = errors.New("management operation panicked")
			}
		}()
		result, err = run(ctx, entry.output)
	}()
	j.mu.Lock()
	defer j.mu.Unlock()
	defer close(entry.done)
	entry.job.State = "succeeded"
	if err != nil {
		entry.job.State, entry.job.Error = "failed", err.Error()
	}
	if ctx.Err() != nil {
		entry.job.State, entry.job.Error = "cancelled", ctx.Err().Error()
	}
	entry.cancel()
	now := time.Now().UTC()
	entry.job.FinishedAt, entry.job.Result = &now, result
	j.active = ""
	if auditErr := j.audit(entry, entry.job.State); auditErr != nil {
		entry.job.State = "failed"
		entry.job.Error = "operation finished, but writing its audit record failed: " + auditErr.Error()
	}
}

func (j *Jobs) audit(entry *runningJob, state string) error {
	// Limit the log to two files of approximately 4 MiB. No args or output are logged.
	if info, err := os.Stat(j.auditPath); err == nil && info.Size() >= 4<<20 {
		if err := os.Rename(j.auditPath, j.auditPath+".1"); err != nil {
			return err
		}
	}
	file, err := os.OpenFile(j.auditPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	return json.NewEncoder(file).Encode(map[string]any{"time": time.Now().UTC(), "id": entry.job.ID, "tool": entry.job.Tool, "source": entry.source, "state": state})
}

func (j *Jobs) Get(id string) (Job, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	entry, ok := j.jobs[id]
	if !ok {
		return Job{}, &Error{Code: "not_found", Message: "job not found; job history is kept in memory", Status: 404}
	}
	job := entry.job
	job.Output, job.Truncated = entry.output.Snapshot()
	return job, nil
}

func (j *Jobs) List() []Job {
	j.mu.Lock()
	defer j.mu.Unlock()
	values := make([]Job, 0, len(j.order))
	for _, id := range j.order {
		job := j.jobs[id].job
		// Listing metadata stays small; fetch one job for output and result.
		job.Result = nil
		values = append(values, job)
	}
	slices.Reverse(values)
	return values
}

func (j *Jobs) Cancel(id string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	entry, ok := j.jobs[id]
	if !ok {
		return &Error{Code: "not_found", Message: "job not found", Status: 404}
	}
	if entry.job.State != "running" {
		return &Error{Code: "conflict", Message: "job has already finished", Status: 409}
	}
	entry.cancel()
	if err := j.audit(entry, "cancel_requested"); err != nil {
		return fmt.Errorf("cancellation requested, but audit write failed: %w", err)
	}
	return nil
}

func (j *Jobs) Close() {
	j.mu.Lock()
	j.closed = true
	var done chan struct{}
	if j.active != "" {
		entry := j.jobs[j.active]
		entry.cancel()
		done = entry.done
	}
	j.mu.Unlock()
	if done != nil {
		<-done
	}
}
