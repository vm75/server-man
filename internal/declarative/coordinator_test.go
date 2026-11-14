package declarative

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeAdapter struct {
	calls   []string
	matches map[string]bool
	fail    string
	invalid bool
	active  bool
}

func (a *fakeAdapter) Validate(context.Context, Config, string) error {
	if a.invalid {
		return errors.New("private native diagnostic")
	}
	return nil
}
func (a *fakeAdapter) Inspect(_ context.Context, _ Config, key string) (Observation, error) {
	return Observation{Registered: a.matches[key], Active: a.active, Matches: a.matches[key]}, nil
}
func (a *fakeAdapter) Apply(_ context.Context, _ Config, _ Config, key, _ string) error {
	a.calls = append(a.calls, key)
	if a.fail == key {
		return errors.New("private diagnostic")
	}
	a.matches[key] = true
	return nil
}
func coordinatorFixture(t *testing.T) (Coordinator, Config, *fakeAdapter) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "data/content"), 0700); err != nil {
		t.Fatal(err)
	}
	cfg, err := Parse([]byte(example))
	if err != nil {
		t.Fatal(err)
	}
	a := &fakeAdapter{matches: map[string]bool{}}
	return Coordinator{Store: Store{Path: filepath.Join(root, "config.yaml")}, Adapters: map[string]Adapter{"podman/quadlet": a}}, cfg, a
}
func TestApplyPersistsAndVerifiesWithoutNoopRestart(t *testing.T) {
	c, cfg, a := coordinatorFixture(t)
	j, err := c.SaveApply(context.Background(), cfg, "missing", "now")
	if err != nil || j.Stage != "complete" {
		t.Fatal(j, err)
	}
	if len(a.calls) != 1 {
		t.Fatal(a.calls)
	}
	if _, err = c.SaveApply(context.Background(), cfg, j.DesiredRevision, "now"); err != nil {
		t.Fatal(err)
	}
	if len(a.calls) != 1 {
		t.Fatal("no-op reapplied", a.calls)
	}
	a.matches["service/website"] = false
	if _, err = c.SaveApply(context.Background(), cfg, j.DesiredRevision, "now"); err != nil {
		t.Fatal(err)
	}
	if len(a.calls) != 2 {
		t.Fatal("drift was ignored")
	}
}
func TestFailedApplyRetainsDesiredAndRetryInspects(t *testing.T) {
	c, cfg, a := coordinatorFixture(t)
	a.fail = "service/website"
	j, err := c.SaveApply(context.Background(), cfg, "missing", "now")
	if err == nil || j.Stage != "failed" || strings.Contains(err.Error(), "private") {
		t.Fatal(j, err)
	}
	snapshot, err := c.Store.Read()
	if err != nil || snapshot.Revision != j.DesiredRevision {
		t.Fatal("desired config lost", err)
	}
	if _, err = c.SaveApply(context.Background(), cfg, snapshot.Revision, "now"); err == nil {
		t.Fatal("overwrote incomplete transition")
	}
	// Model an interrupted operation that succeeded natively but whose outcome
	// was not journaled. Retry must observe rather than execute it twice.
	a.matches["service/website"] = true
	a.fail = ""
	j, err = c.Retry(context.Background())
	if err != nil || j.Stage != "complete" {
		t.Fatal(j, err)
	}
	if len(a.calls) != 1 {
		t.Fatal("blind replay", a.calls)
	}
}
func TestInvalidNativeCandidateCannotReplaceConfig(t *testing.T) {
	c, cfg, a := coordinatorFixture(t)
	a.invalid = true
	if _, err := c.SaveApply(context.Background(), cfg, "missing", "now"); err == nil || strings.Contains(err.Error(), "private") {
		t.Fatal(err)
	}
	if _, err := os.Stat(c.Store.Path); !os.IsNotExist(err) {
		t.Fatal("published invalid candidate")
	}
	if _, err := c.ReadJournal(); !os.IsNotExist(err) {
		t.Fatal("journal created before candidate validation")
	}
}
func TestUnavailableAdapterAndMissingFilesRemainBlocked(t *testing.T) {
	c, cfg, _ := coordinatorFixture(t)
	c.Adapters = nil
	j, err := c.SaveApply(context.Background(), cfg, "missing", "now")
	if err == nil || j.Stage != "blocked" {
		t.Fatal(j, err)
	}
	c, cfg, _ = coordinatorFixture(t)
	if err = os.RemoveAll(filepath.Join(filepath.Dir(c.Store.Path), "data")); err != nil {
		t.Fatal(err)
	}
	j, err = c.SaveApply(context.Background(), cfg, "missing", "now")
	if err == nil || j.Stage != "blocked" {
		t.Fatal(j, err)
	}
	if _, err = c.Store.Read(); err != nil {
		t.Fatal("blocked desired state missing", err)
	}
}
func TestPreparedRecoveryAndRevisionConflict(t *testing.T) {
	c, cfg, _ := coordinatorFixture(t)
	_, _, p, err := c.prepare(cfg, "missing")
	if err != nil {
		t.Fatal(err)
	}
	j := Journal{Version: 1, BaseRevision: "missing", DesiredRevision: p.DesiredRevision, Stage: "prepared", Mode: "now", Resources: map[string]ResourceResult{}}
	if err = c.writeJournal(&j); err != nil {
		t.Fatal(err)
	}
	j, err = c.Retry(context.Background())
	if err != nil || j.Stage != "aborted" {
		t.Fatal(j, err)
	}
	j, err = c.SaveApply(context.Background(), cfg, "missing", "now")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(c.Store.Path, []byte("version: 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Retry(context.Background()); !errors.Is(err, ErrConflict) {
		t.Fatal("external edits ignored", err)
	}
}
func TestPreCancelledApplyDoesNotPublish(t *testing.T) {
	c, cfg, _ := coordinatorFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.SaveApply(ctx, cfg, "missing", "now"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := os.Stat(c.Store.Path); !os.IsNotExist(err) {
		t.Fatal("cancelled apply published")
	}
}
func TestImpactIncludesBindingChangesAndDependencyOrder(t *testing.T) {
	c, cfg, _ := coordinatorFixture(t)
	svc := cfg.Services["website"]
	svc.DependsOn = []string{"database"}
	cfg.Services["website"] = svc
	db := svc
	db.DependsOn = nil
	cfg.Services["database"] = db
	_, _, p, err := c.prepare(cfg, "missing")
	if err != nil {
		t.Fatal(err)
	}
	if p.Changes[0].Resource != "service/database" {
		t.Fatal(p)
	}
	j, err := c.SaveApply(context.Background(), cfg, "missing", "now")
	if err != nil {
		t.Fatal(err)
	}
	st := cfg.Storage["content"]
	st.Path = "data/relocated"
	cfg.Storage["content"] = st
	_, _, p, err = c.prepare(cfg, j.DesiredRevision)
	if err != nil || len(p.Changes) != 2 {
		t.Fatal("storage change not propagated", p, err)
	}
}

func TestPartialApplyRetriesOnlyUnmatchedResources(t *testing.T) {
	c, cfg, a := coordinatorFixture(t)
	cfg.Services["another"] = cfg.Services["website"]
	a.fail = "service/website"
	j, err := c.SaveApply(context.Background(), cfg, "missing", "now")
	if err == nil || j.Resources["service/another"].State != "registered" {
		t.Fatal(j, err)
	}
	a.fail = ""
	j, err = c.Retry(context.Background())
	if err != nil || j.Stage != "complete" {
		t.Fatal(j, err)
	}
	count := 0
	for _, key := range a.calls {
		if key == "service/another" {
			count++
		}
	}
	if count != 1 {
		t.Fatal("successful resource reapplied", a.calls)
	}
}
func TestInterruptedRemovalIsNotReplayed(t *testing.T) {
	c, cfg, a := coordinatorFixture(t)
	j, err := c.SaveApply(context.Background(), cfg, "missing", "now")
	if err != nil {
		t.Fatal(err)
	}
	a.fail = "service/website"
	empty := Config{Version: 1}
	j, err = c.SaveApply(context.Background(), empty, j.DesiredRevision, "now")
	if err == nil || j.Stage != "failed" {
		t.Fatal(j, err)
	}
	before := len(a.calls)
	if _, err = c.Retry(context.Background()); err == nil {
		t.Fatal("uncertain removal replay accepted")
	}
	if len(a.calls) != before {
		t.Fatal("destructive operation repeated")
	}
}
func TestJournalSymlinkAndPublicStateRejected(t *testing.T) {
	c, cfg, _ := coordinatorFixture(t)
	other := filepath.Join(t.TempDir(), "state")
	if err := os.WriteFile(other, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, c.Store.Path+".apply.json"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SaveApply(context.Background(), cfg, "missing", "now"); err == nil {
		t.Fatal("journal symlink accepted")
	}
	data, err := os.ReadFile(other)
	if err != nil || string(data) != "unchanged" {
		t.Fatal("foreign state modified")
	}
}
