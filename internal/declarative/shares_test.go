package declarative

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"server-man/internal/core"
	"server-man/internal/modules/shares"
)

type shareHelperFixture struct {
	calls              []string
	definitions        []shares.Definition
	revision           string
	discoveryAvailable bool
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
	return shares.DefinitionState{Revision: h.revision, Available: true, DiscoveryAvailable: h.discoveryAvailable, Matches: false, RuntimeMatches: true}, nil
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

func TestShareStatusesServerAvailability(t *testing.T) {
	c, cfg, _ := coordinatorFixture(t)
	sambaHelper := &shareHelperFixture{revision: "samba-rev"}
	c.Adapters["samba"] = &ShareAdapter{Store: c.Store, Protocol: "samba", Helper: sambaHelper}
	if _, err := c.Store.Save(cfg, "missing"); err != nil {
		t.Fatal(err)
	}
	statuses, err := c.ShareStatuses(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	st, ok := statuses.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", statuses)
	}
	servers, ok := st["servers"].(map[string]map[string]any)
	if !ok {
		t.Fatalf("expected servers map, got %T", st["servers"])
	}
	if s, ok := servers["samba"]; !ok || s["available"] != true {
		t.Fatalf("expected samba server available=true, got %v", s)
	} else if s["discoveryAvailable"] != false {
		t.Fatalf("expected missing discovery server from helper, got %v", s["discoveryAvailable"])
	}
	if n, ok := servers["nfs"]; !ok || n["available"] != false {
		t.Fatalf("expected nfs server available=false without adapter, got %v", n)
	}
	sambaHelper.discoveryAvailable = true
	statuses, err = c.ShareStatuses(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	servers = statuses.(map[string]any)["servers"].(map[string]map[string]any)
	if servers["samba"]["discoveryAvailable"] != true {
		t.Fatal("discovery server availability was not propagated from helper", servers)
	}
}

func TestShareEditWithManualPathDerivesStorage(t *testing.T) {
	c, cfg, _ := coordinatorFixture(t)
	snap, err := c.Store.Save(cfg, "missing")
	if err != nil {
		t.Fatal(err)
	}
	shareJSON := `{"protocol":"samba","path":"/srv/shared_media","enabled":true,"readOnly":false}`
	updated, err := c.EditResource("share", "media", "save", shareJSON, snap.Revision)
	if err != nil {
		t.Fatal(err)
	}
	sh, ok := updated.Shares["media"]
	if !ok {
		t.Fatal("media share not found in updated config")
	}
	if sh.Storage != "media" {
		t.Fatalf("expected storage 'media', got %q", sh.Storage)
	}
	st, ok := updated.Storage["media"]
	if !ok {
		t.Fatal("derived storage 'media' not found in config")
	}
	if st.Path != "/srv/shared_media" || !st.External || st.Kind != "directory" {
		t.Fatalf("unexpected storage properties: %+v", st)
	}
}

func TestDirectoryBrowseSafeListing(t *testing.T) {
	c, _, _ := coordinatorFixture(t)
	r := core.NewRegistry(false, nil)
	if err := RegisterShareStatus(r, c); err != nil {
		t.Fatal(err)
	}
	res, err := r.Invoke(context.Background(), "managed_directory_browse", json.RawMessage(`{"path":"/"}`), "test")
	if err != nil {
		t.Fatal(err)
	}
	m, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", res)
	}
	dirs, ok := m["directories"].([]string)
	if !ok || len(dirs) == 0 {
		t.Fatalf("expected directories list from /, got %v", m["directories"])
	}
}

func TestSambaUsesProcessAccountInsteadOfLegacyUsers(t *testing.T) {
	c, cfg, _ := coordinatorFixture(t)
	account, err := shares.CurrentAccount()
	if err != nil {
		t.Fatal(err)
	}
	for _, legacy := range [][]string{nil, {"another", "@staff"}} {
		cfg.Shares = map[string]Share{"documents": {Protocol: "samba", Storage: "content", Enabled: true, Users: legacy}}
		if err := cfg.Validate(); err != nil {
			t.Fatal(err)
		}
		adapter := &ShareAdapter{Store: c.Store, Protocol: "samba"}
		defs, err := adapter.definitions(cfg, false)
		if err != nil || len(defs) != 1 || len(defs[0].Users) != 1 || defs[0].Users[0] != account {
			t.Fatal("share did not bind process account", err)
		}
	}
	cfg.Shares = map[string]Share{"documents": {Protocol: "samba", Storage: "content", Enabled: true, Guest: true}}
	adapter := &ShareAdapter{Store: c.Store, Protocol: "samba"}
	defs, err := adapter.definitions(cfg, false)
	if err != nil || len(defs[0].Users) != 0 {
		t.Fatal("guest share received authenticated restriction", err)
	}
}
