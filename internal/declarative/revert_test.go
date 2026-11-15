package declarative

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type revisionAdapter struct {
	native map[string]string
	calls  int
	fail   bool
}

func (a *revisionAdapter) Validate(context.Context, Config, string) error { return nil }
func (a *revisionAdapter) Inspect(_ context.Context, c Config, key string) (Observation, error) {
	fp := a.native[key]
	return Observation{Registered: fp != "", Matches: fp == resourceFingerprint(c, key)}, nil
}
func (a *revisionAdapter) Apply(_ context.Context, _, desired Config, key, _ string) error {
	a.calls++
	if a.fail {
		return ErrConflict
	}
	a.native[key] = resourceFingerprint(desired, key)
	return nil
}
func revertFixture(t *testing.T) (Coordinator, Config, *revisionAdapter) {
	c, cfg, _ := coordinatorFixture(t)
	a := &revisionAdapter{native: map[string]string{}}
	c.Adapters["podman/quadlet"] = a
	return c, cfg, a
}
func TestRevertFailedUpdateReconcilesHistoricalDesiredWithoutStarting(t *testing.T) {
	c, cfg, a := revertFixture(t)
	ctx := context.Background()
	first, err := c.SaveApply(ctx, cfg, "missing", "now")
	if err != nil {
		t.Fatal(err)
	}
	service := cfg.Services["website"]
	service.Description = "changed"
	cfg.Services["website"] = service
	a.fail = true
	failed, err := c.SaveApply(ctx, cfg, first.DesiredRevision, "now")
	if err == nil || failed.Stage != "failed" {
		t.Fatal(failed, err)
	}
	calls := a.calls
	if _, err = c.Revert(ctx, first.DesiredRevision, first.DesiredRevision, "next_start"); err != ErrConflict {
		t.Fatal("stale revert accepted", err)
	}
	current, _ := c.Store.Read()
	if current.Revision != failed.DesiredRevision {
		t.Fatal("stale revert changed desired state")
	}
	a.fail = false
	restored, err := c.Revert(ctx, first.DesiredRevision, failed.DesiredRevision, "next_start")
	if err != nil || restored.Stage != "complete" {
		t.Fatal(restored, err)
	}
	if restored.DesiredRevision != first.DesiredRevision || a.calls != calls {
		t.Fatal("matching native revision was needlessly reapplied", restored, a.calls)
	}
	entries, err := c.Store.History()
	if err != nil {
		t.Fatal(err)
	}
	retained := false
	for _, entry := range entries {
		if entry.Revision == failed.DesiredRevision {
			retained = true
		}
	}
	if !retained {
		t.Fatal("failed desired revision was not retained")
	}
}
func TestRevertCannotAbandonAnUnresolvedRemoval(t *testing.T) {
	c, cfg, a := revertFixture(t)
	ctx := context.Background()
	empty := Config{Version: 1}
	initial, err := c.SaveApply(ctx, empty, "missing", "now")
	if err != nil {
		t.Fatal(err)
	}
	created, err := c.SaveApply(ctx, cfg, initial.DesiredRevision, "now")
	if err != nil {
		t.Fatal(err)
	}
	a.fail = true
	failed, err := c.SaveApply(ctx, empty, created.DesiredRevision, "now")
	if err == nil {
		t.Fatal("expected failed removal")
	}
	if _, err = c.PreviewRevert(ctx, initial.DesiredRevision, failed.DesiredRevision); err == nil || !strings.Contains(err.Error(), "unresolved removal") {
		t.Fatal("abandoned native resource", err)
	}
	journal, _ := c.ReadJournal()
	if journal.Stage != "failed" {
		t.Fatal("failed revert replaced recovery journal")
	}
	a.fail = false
	if _, err = c.Revert(ctx, created.DesiredRevision, failed.DesiredRevision, "next_start"); err != nil {
		t.Fatal("could not restore unresolved resource", err)
	}
}
func TestRevertPreparedCrashRetainsPriorRecoveryUntilDesiredCommit(t *testing.T) {
	for _, saved := range []bool{false, true} {
		t.Run(map[bool]string{false: "before-save", true: "after-save"}[saved], func(t *testing.T) {
			c, cfg, a := revertFixture(t)
			ctx := context.Background()
			first, err := c.SaveApply(ctx, cfg, "missing", "now")
			if err != nil {
				t.Fatal(err)
			}
			service := cfg.Services["website"]
			service.Description = "changed"
			cfg.Services["website"] = service
			a.fail = true
			failed, err := c.SaveApply(ctx, cfg, first.DesiredRevision, "now")
			if err == nil {
				t.Fatal("expected failed update")
			}
			raw, err := c.Store.ReadHistory(first.DesiredRevision)
			if err != nil {
				t.Fatal(err)
			}
			original, err := Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			j := Journal{Version: 1, BaseRevision: failed.DesiredRevision, DesiredRevision: first.DesiredRevision, Stage: "prepared", Mode: "next_start", Superseded: &failed, Resources: map[string]ResourceResult{"service/website": {Fingerprint: resourceFingerprint(original, "service/website"), State: "pending"}}}
			if err = c.writeJournal(&j); err != nil {
				t.Fatal(err)
			}
			if saved {
				if _, err = c.Store.Save(original, failed.DesiredRevision); err != nil {
					t.Fatal(err)
				}
			}
			a.fail = false
			recovered, err := c.Retry(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if saved {
				if recovered.Stage != "complete" || recovered.DesiredRevision != first.DesiredRevision {
					t.Fatal(recovered)
				}
			} else {
				if recovered.Stage != "failed" || recovered.DesiredRevision != failed.DesiredRevision {
					t.Fatal("lost prior recovery", recovered)
				}
			}
		})
	}
}
func TestHistoryRejectsUnsafeEntriesAndReportsInvalidYAML(t *testing.T) {
	c, _, _ := revertFixture(t)
	if err := os.Mkdir(c.Store.Path+".history", 0700); err != nil {
		t.Fatal(err)
	}
	if entries, err := c.Store.History(); err != nil || len(entries) != 0 {
		t.Fatal(entries, err)
	}
	raw := []byte("private-content: [")
	revision := digest(raw)
	path := filepath.Join(c.Store.Path+".history", revision+".yaml")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	entries, err := c.Store.History()
	if err != nil || len(entries) != 1 || entries[0].Valid || strings.Contains(entries[0].Error, "private-content") {
		t.Fatal(entries, err)
	}
	if got, err := c.Store.ReadHistory(revision); err != nil || string(got) != string(raw) {
		t.Fatal(err)
	}
	if err = os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Store.ReadHistory(revision); err == nil {
		t.Fatal("read public history entry")
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(c.Store.Path, path); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Store.ReadHistory(revision); err == nil {
		t.Fatal("followed symlink history entry")
	}
	if _, err = c.Store.ReadHistory("../outside"); err == nil {
		t.Fatal("accepted traversal revision")
	}
}

func TestRepairDoesNotUsePartiallyDecodedInvalidDefinitions(t *testing.T) {
	c, _, a := revertFixture(t)
	raw := []byte(example + "\nunknown-setting: true\n")
	if err := os.WriteFile(c.Store.Path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	p, err := c.Preview(context.Background(), Config{Version: 1}, digest(raw))
	if err != nil || len(p.Changes) != 0 {
		t.Fatal("invalid YAML authorized a native removal", p, err)
	}
	if _, err = c.SaveApply(context.Background(), Config{Version: 1}, digest(raw), "now"); err != nil || a.calls != 0 {
		t.Fatal("repair executed invalid prior definitions", a.calls, err)
	}
}

func TestFailedRevertSaveKeepsPriorJournalRecoverable(t *testing.T) {
	c, cfg, a := revertFixture(t)
	ctx := context.Background()
	initial, err := c.SaveApply(ctx, cfg, "missing", "now")
	if err != nil {
		t.Fatal(err)
	}
	service := cfg.Services["website"]
	service.Description = "changed"
	cfg.Services["website"] = service
	a.fail = true
	failed, err := c.SaveApply(ctx, cfg, initial.DesiredRevision, "now")
	if err == nil {
		t.Fatal("expected failed update")
	}
	// A damaged existing history entry forces Save to fail after durable revert
	// preparation but before changing the current desired configuration.
	damaged := filepath.Join(c.Store.Path+".history", failed.DesiredRevision+".yaml")
	if err = os.WriteFile(damaged, []byte("damaged"), 0600); err != nil {
		t.Fatal(err)
	}
	a.fail = false
	if _, err = c.Revert(ctx, initial.DesiredRevision, failed.DesiredRevision, "next_start"); err == nil {
		t.Fatal("unsafe history accepted")
	}
	prepared, err := c.ReadJournal()
	if err != nil || prepared.Stage != "prepared" || prepared.Superseded == nil {
		t.Fatal(prepared, err)
	}
	recovered, err := c.Retry(ctx)
	if err != nil || recovered.Stage != "failed" || recovered.DesiredRevision != failed.DesiredRevision {
		t.Fatal("lost original recovery transition", recovered, err)
	}
}
