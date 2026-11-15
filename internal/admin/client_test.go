package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"server-man/internal/core"
	"strings"
	"testing"
)

type clientFixture struct {
	t        *testing.T
	calls    int
	request  Request
	response string
}

func (f *clientFixture) Run(context.Context, core.Command, io.Writer) error {
	f.t.Fatal("unexpected Run")
	return nil
}
func (f *clientFixture) Capture(_ context.Context, c core.Command) (string, error) {
	f.calls++
	if c.Program != "/usr/bin/sudo" || !reflect.DeepEqual(c.Args, []string{"-n", "--", HelperPath}) {
		f.t.Fatalf("unexpected command: %s %v", c.Program, c.Args)
	}
	if err := json.NewDecoder(c.Stdin).Decode(&f.request); err != nil {
		f.t.Fatal(err)
	}
	return f.response, nil
}
func TestClientPreservesErrorAndOutput(t *testing.T) {
	f := &clientFixture{t: t, response: `{"error":"stale revision","code":"conflict","status":409,"output":"rollback complete"}`}
	var out strings.Builder
	_, err := (Client{Runner: f}).Call(context.Background(), "shares_definitions_apply", core.Input{"name": "docs", "revision": "stale", "confirm": true}, &out)
	var typed *core.Error
	if !errors.As(err, &typed) || typed.Status != 409 || typed.Code != "conflict" {
		t.Fatal(err)
	}
	if out.String() != "rollback complete" || f.request.Operation != "shares_definitions_apply" {
		t.Fatal(out.String(), f.request)
	}
}
func TestRouteRetainsValidationAndUserScope(t *testing.T) {
	f := &clientFixture{t: t, response: `{"result":{"scope":"system"}}`}
	r := core.NewRegistry(false, nil)
	err := r.AddModule(core.Module{ID: "services", Available: true}, core.Tool{Name: "services_list", ReadOnly: true, Fields: map[string]core.Field{"scope": core.EnumField("scope", false, "system", "user")}, Run: func(_ context.Context, in core.Input, _ io.Writer) (any, error) { return "local user", nil }})
	if err != nil {
		t.Fatal(err)
	}
	Route(r, Client{Runner: f}, "system")
	result, err := r.Invoke(context.Background(), "services_list", json.RawMessage(`{"scope":"user"}`), "test")
	if err != nil || result != "local user" || f.calls != 0 {
		t.Fatal(result, err, f.calls)
	}
	_, err = r.Invoke(context.Background(), "services_list", json.RawMessage(`{"scope":"root"}`), "test")
	if err == nil || f.calls != 0 {
		t.Fatal("invalid scope reached helper")
	}
	_, err = r.Invoke(context.Background(), "services_list", json.RawMessage(`{"scope":"system"}`), "test")
	if err != nil || f.calls != 1 {
		t.Fatal(err, f.calls)
	}
}

func TestRouteWritePolicyPreventsElevation(t *testing.T) {
	f := &clientFixture{t: t}
	r := core.NewRegistry(false, nil)
	err := r.AddModule(core.Module{ID: "updates", Available: true}, core.Tool{Name: "packages_install", Destructive: true, Run: func(context.Context, core.Input, io.Writer) (any, error) { t.Fatal("local write ran"); return nil, nil }})
	if err != nil {
		t.Fatal(err)
	}
	Route(r, Client{Runner: f}, "system")
	_, err = r.Invoke(context.Background(), "packages_install", json.RawMessage(`{"confirm":true}`), "test")
	var typed *core.Error
	if !errors.As(err, &typed) || typed.Code != "writes_disabled" || f.calls != 0 {
		t.Fatal(err, f.calls)
	}
}

func TestClientRetainsPrivilegedTruncationNotice(t *testing.T) {
	encoded, err := json.Marshal(Response{Output: strings.Repeat("x", 128<<10), Truncated: true})
	if err != nil {
		t.Fatal(err)
	}
	f := &clientFixture{t: t, response: string(encoded)}
	out := core.NewTail(128 << 10)
	if _, err = (Client{Runner: f}).Call(context.Background(), "updates_install", core.Input{"confirm": true}, out); err != nil {
		t.Fatal(err)
	}
	text, truncated := out.Snapshot()
	if !truncated || !strings.Contains(text, "Earlier privileged output was discarded") {
		t.Fatal("privileged truncation was hidden")
	}
}
