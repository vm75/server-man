package shares

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"server-man/internal/core"
)

func TestAuthoritativeReplacementRemovesStaleOutputs(t *testing.T) {
	for _, protocol := range []string{"samba", "nfs"} {
		t.Run(protocol, func(t *testing.T) {
			b, r, data := fixture(t)
			first := definition(protocol, data)
			rev := revision(t, b, protocol)
			if _, err := b.ReplaceDefinitions(context.Background(), protocol, rev, []Definition{first}, io.Discard); err != nil {
				t.Fatal(err)
			}
			state, err := b.InspectDefinitions(context.Background(), protocol, []Definition{first})
			if err != nil || !state.Matches || !state.RuntimeMatches {
				t.Fatal(state, err)
			}
			if _, err = b.ReplaceDefinitions(context.Background(), protocol, rev, nil, io.Discard); err == nil {
				t.Fatal("stale revision accepted")
			}
			if _, err = b.ReplaceDefinitions(context.Background(), protocol, state.Revision, nil, io.Discard); err != nil {
				t.Fatal(err)
			}
			state, err = b.InspectDefinitions(context.Background(), protocol, nil)
			if err != nil || !state.Matches || !state.RuntimeMatches {
				t.Fatal(state, err)
			}
			if _, err = os.Stat(data); err != nil {
				t.Fatal("shared data removed")
			}
			for _, entry := range r.active() {
				if entry.path == "/legacy" {
					return
				}
			}
			if protocol == "nfs" {
				t.Fatal("unmanaged runtime export removed")
			}
		})
	}
}
func TestAuthoritativeSetRollbackPreservesPriorNativeState(t *testing.T) {
	b, r, data := fixture(t)
	first := definition("samba", data)
	if _, err := b.ReplaceDefinitions(context.Background(), "samba", revision(t, b, "samba"), []Definition{first}, io.Discard); err != nil {
		t.Fatal(err)
	}
	previous, err := os.ReadFile(b.sambaManaged)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	r.beforeRun = func(_ context.Context, c core.Command) error {
		if c.Program == "smbcontrol" {
			calls++
			if calls == 1 {
				return errors.New("fixture reload failure")
			}
		}
		return nil
	}
	if _, err = b.ReplaceDefinitions(context.Background(), "samba", revision(t, b, "samba"), nil, io.Discard); err == nil {
		t.Fatal("failed apply reported success")
	}
	current, err := os.ReadFile(b.sambaManaged)
	if err != nil || string(current) != string(previous) {
		t.Fatal("previous output not restored", err)
	}
}
func TestAuthoritativeSetRejectsUnmanagedCollision(t *testing.T) {
	b, _, data := fixture(t)
	d := definition("samba", data)
	d.Name = "legacy"
	if _, err := b.ReplaceDefinitions(context.Background(), "samba", revision(t, b, "samba"), []Definition{d}, io.Discard); err == nil || !strings.Contains(err.Error(), "unmanaged") {
		t.Fatal(err)
	}
}

func TestAuthoritativeCandidateValidationDoesNotPublishOrReload(t *testing.T) {
	for _, protocol := range []string{"samba", "nfs"} {
		t.Run(protocol, func(t *testing.T) {
			b, r, data := fixture(t)
			if err := b.ValidateDefinitions(context.Background(), protocol, []Definition{definition(protocol, data)}); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(b.file(protocol)); !os.IsNotExist(err) {
				t.Fatal("validation published fragment")
			}
			for _, cmd := range r.recorded() {
				if cmd.Program == "smbcontrol" || cmd.Program == "exportfs" && len(cmd.Args) > 0 && cmd.Args[0] != "-s" {
					t.Fatal("validation changed runtime", cmd)
				}
			}
		})
	}
}

func TestUnpublishMissingDataDoesNotCreateEmptyReplacement(t *testing.T) {
	for _, protocol := range []string{"samba", "nfs"} {
		t.Run(protocol, func(t *testing.T) {
			b, _, data := fixture(t)
			d := definition(protocol, data)
			if _, err := b.ReplaceDefinitions(context.Background(), protocol, revision(t, b, protocol), []Definition{d}, io.Discard); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(data); err != nil {
				t.Fatal(err)
			}
			if _, err := b.ReplaceDefinitions(context.Background(), protocol, revision(t, b, protocol), nil, io.Discard); err != nil {
				t.Fatal("missing data blocked unpublication", err)
			}
			if _, err := os.Stat(data); !os.IsNotExist(err) {
				t.Fatal("unpublication created replacement data")
			}
		})
	}
}
