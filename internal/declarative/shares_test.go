package declarative

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"serverman/internal/core"
	"serverman/internal/modules/shares"
)

type shareHelperFixture struct {
	calls       []string
	definitions []shares.Definition
	revision    string
}

func (h *shareHelperFixture) Call(_ context.Context, operation string, in core.Input, _ io.Writer) (any, error) {
	h.calls = append(h.calls, operation)
	var defs []shares.Definition
	if err := json.Unmarshal([]byte(in.String("definitions")), &defs); err != nil {
		return nil, err
	}
	if operation == "shares_definitions_apply" {
		if !in.Bool("confirm") || in.String("revision") != h.revision {
			return nil, errors.New("bad native revision or confirmation")
		}
		h.definitions = defs
		return map[string]any{"revision": "new"}, nil
	}
	return shares.DefinitionState{Revision: h.revision, Available: true, Matches: false, RuntimeMatches: true}, nil
}
func TestCentralShareAdapterUsesOnlyDesiredPublishedDefinitions(t *testing.T) {
	c, cfg, _ := coordinatorFixture(t)
	cfg.Shares = map[string]Share{"documents": {Protocol: "nfs", Storage: "content", Enabled: true, ReadOnly: true, Clients: []string{"10.0.0.0/24"}}, "disabled": {Protocol: "nfs", Storage: "content", Enabled: false, ReadOnly: true, Clients: []string{"10.0.0.0/24"}}}
	helper := &shareHelperFixture{revision: "native-revision"}
	adapter := &ShareAdapter{Store: c.Store, Protocol: "nfs", Helper: helper}
	if err := adapter.Validate(context.Background(), cfg, "share/documents"); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Apply(context.Background(), Config{Version: 1}, cfg, "share/documents", "now"); err != nil {
		t.Fatal(err)
	}
	if len(helper.definitions) != 1 || helper.definitions[0].Name != "documents" || !helper.definitions[0].RootSquash || helper.definitions[0].Security != "sys" {
		t.Fatal(helper.definitions)
	}
	if helper.definitions[0].Path != c.Store.Resolve("data/content") {
		t.Fatal("relative path not rebound")
	}
}
func TestCentralShareNormalizationRejectsNativeInjection(t *testing.T) {
	c, cfg, _ := coordinatorFixture(t)
	cfg.Shares = map[string]Share{"global": {Protocol: "samba", Storage: "content", Enabled: true, Users: []string{"alice"}}}
	helper := &shareHelperFixture{}
	adapter := &ShareAdapter{Store: c.Store, Protocol: "samba", Helper: helper}
	if err := adapter.Validate(context.Background(), cfg, "share/global"); err == nil {
		t.Fatal("reserved native section accepted")
	}
	if len(helper.calls) != 0 {
		t.Fatal("invalid native definition reached privileged helper")
	}
}
