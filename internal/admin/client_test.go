package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"serverman/internal/core"
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
	_, err := (Client{Runner: f}).Call(context.Background(), "samba_share_remove", core.Input{"name": "docs", "revision": "stale", "confirm": true}, &out)
	var typed *core.Error
	if !errors.As(err, &typed) || typed.Status != 409 || typed.Code != "conflict" {
		t.Fatal(err)
	}
	if out.String() != "rollback complete" || f.request.Operation != "samba_share_remove" {
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
