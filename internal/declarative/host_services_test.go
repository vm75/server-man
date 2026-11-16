package declarative

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"testing"

	"server-man/internal/core"
)

type actionAdapter struct {
	fakeAdapter
	actions []string
}

func (a *actionAdapter) Action(_ context.Context, _ Config, key, action string) error {
	a.actions = append(a.actions, key+":"+action)
	return nil
}
func TestHostManagedUnitRoutesApplicationButOrdinarySystemUnitsStayDirect(t *testing.T) {
	c, cfg, _ := coordinatorFixture(t)
	if _, err := c.Store.Save(cfg, "missing"); err != nil {
		t.Fatal(err)
	}
	adapter := &actionAdapter{fakeAdapter: fakeAdapter{matches: map[string]bool{}}}
	c.Adapters["podman/quadlet"] = adapter
	r := core.NewRegistry(true, nil)
	direct := 0
	if err := r.AddModule(core.Module{ID: "services", Available: true}, core.Tool{Name: "services_stop", Destructive: true, Fields: map[string]core.Field{"unit": core.StringField("Unit", true, 255), "scope": core.EnumField("Scope", true, "user", "system")}, Run: func(context.Context, core.Input, io.Writer) (any, error) {
		direct++
		return map[string]bool{"direct": true}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	RouteManagedUnits(r, c, "system")
	unit := quadletUnits(cfg, "website")[0]
	for _, scope := range []string{"user", "system"} {
		raw, _ := json.Marshal(map[string]any{"unit": unit, "scope": scope, "confirm": true})
		if _, err := r.Execute(context.Background(), "services_stop", raw, io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := json.Marshal(map[string]any{"unit": "sshd.service", "scope": "user", "confirm": true})
	if _, err := r.Execute(context.Background(), "services_stop", raw, io.Discard); err != nil {
		t.Fatal(err)
	}
	if direct != 2 || len(adapter.actions) != 1 || adapter.actions[0] != "service/website:stop" {
		t.Fatal(direct, adapter.actions)
	}
}

func TestManagedUnitDetailsEnablementAndBrokenConfiguration(t *testing.T) {
	c, cfg, _ := coordinatorFixture(t)
	adapter := &actionAdapter{fakeAdapter: fakeAdapter{matches: map[string]bool{}}}
	c.Adapters["podman/quadlet"] = adapter
	if _, err := c.SaveApply(context.Background(), cfg, "missing", "now"); err != nil {
		t.Fatal(err)
	}
	r := core.NewRegistry(true, nil)
	direct := 0
	for _, action := range []string{"details", "disable", "stop", "reload"} {
		if err := r.AddModule(core.Module{ID: action, Available: true}, core.Tool{Name: "services_" + action, ReadOnly: action == "details", Fields: map[string]core.Field{"unit": core.StringField("Unit", true, 255)}, Run: func(context.Context, core.Input, io.Writer) (any, error) {
			direct++
			return map[string]string{"name": "unit"}, nil
		}}); err != nil {
			t.Fatal(err)
		}
	}
	RouteManagedUnits(r, c, "user")
	unit := quadletUnits(cfg, "website")[0]
	raw, _ := json.Marshal(map[string]any{"unit": unit})
	result, err := r.Execute(context.Background(), "services_details", raw, io.Discard)
	if err != nil || result.(map[string]any)["managedApplication"] != "website" {
		t.Fatal(result, err)
	}
	if _, err = r.Execute(context.Background(), "services_disable", raw, io.Discard); err != nil {
		t.Fatal(err)
	}
	current, err := c.Store.Read()
	if err != nil || current.Config.Services["website"].Enabled || len(adapter.actions) != 0 {
		t.Fatal(current, adapter.actions, err)
	}
	if _, err = r.Execute(context.Background(), "services_reload", raw, io.Discard); err == nil {
		t.Fatal("direct reload bypassed managed apply")
	}
	if err = os.WriteFile(c.Store.Path, []byte("invalid: ["), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Execute(context.Background(), "services_stop", raw, io.Discard); err == nil || direct != 1 {
		t.Fatal("broken configuration bypassed managed lifecycle", direct, err)
	}
}

// The reserved prefix cannot be used to bypass the authoritative coordinator
// after a resource is renamed, removed, or a generated unit is left behind.
func TestReservedGeneratedUserUnitsCannotUseDirectHostWrites(t *testing.T) {
	c, cfg, _ := coordinatorFixture(t)
	if _, err := c.Store.Save(cfg, "missing"); err != nil {
		t.Fatal(err)
	}
	r := core.NewRegistry(true, nil)
	direct := 0
	if err := r.AddModule(core.Module{ID: "services", Available: true}, core.Tool{
		Name: "services_restart", Destructive: true,
		Fields: map[string]core.Field{"unit": core.StringField("Unit", true, 255), "scope": core.EnumField("Scope", false, "user", "system")},
		Run: func(context.Context, core.Input, io.Writer) (any, error) {
			direct++
			return map[string]bool{"direct": true}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	RouteManagedUnits(r, c, "user")
	for _, tc := range []struct{
		unit string
		scope string
		denied bool
	}{
		{"sm-compose-orphan.service", "user", true},
		{"sm-orphan-aabbcc.service", "user", true},
		{"sshd.service", "user", false},
		{"sm-compose-orphan.service", "system", false},
		{"sm-orphan-aabbcc.service", "", true},
	} {
		input := map[string]any{"unit": tc.unit, "confirm": true}
		if tc.scope != "" {
			input["scope"] = tc.scope
		}
		raw, _ := json.Marshal(input)
		_, err := r.Execute(context.Background(), "services_restart", raw, io.Discard)
		if (err != nil) != tc.denied {
			t.Fatalf("%s/%s: denied=%t, err=%v", tc.scope, tc.unit, tc.denied, err)
		}
	}
	if direct != 2 {
		t.Fatalf("unknown reserved user units bypassed coordinator: %d direct actions", direct)
	}
}
