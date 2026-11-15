package declarative

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"server-man/internal/core"
)

func TestTypedEditingStaleReferencesAndFieldValidation(t *testing.T) {
	c, cfg, _ := coordinatorFixture(t)
	snapshot, err := c.Store.Save(cfg, "missing")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.EditResource("storage", "content", "remove", "", snapshot.Revision); err == nil {
		t.Fatal("dangling storage reference accepted")
	}
	if _, err = c.EditResource("storage", "new", "save", `{"path":"data/new","shell":"bad"}`, snapshot.Revision); err == nil {
		t.Fatal("unknown field accepted")
	}
	if _, err = c.EditResource("storage", "new", "save", `{"path":"data/new"}`, "missing"); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	next, err := c.EditResource("storage", "new", "save", `{"path":"data/new"}`, snapshot.Revision)
	if err != nil || next.Storage["new"].Path != "data/new" {
		t.Fatal(next, err)
	}
	disk, err := c.Store.Read()
	if err != nil || len(disk.Config.Storage) != 1 {
		t.Fatal("editing preview persisted config", err)
	}
}
func TestTypedFirstResourceAndPrivateReadPolicy(t *testing.T) {
	c, _, _ := coordinatorFixture(t)
	next, err := c.EditResource("storage", "documents", "save", `{"path":"data/documents"}`, "missing")
	if err != nil || next.Version != 1 {
		t.Fatal(next, err)
	}
	if _, err = os.Stat(c.Store.Path); !os.IsNotExist(err) {
		t.Fatal("form edit created file")
	}
	r := core.NewRegistry(false, nil)
	if err = RegisterEditing(r, c); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Invoke(context.Background(), "managed_config_model", json.RawMessage(`{}`), "test"); err == nil {
		t.Fatal("private model read bypassed write policy")
	}
	args, _ := json.Marshal(map[string]any{"kind": "storage", "id": "documents", "operation": "save", "definition": `{"path":"data/documents"}`, "revision": "missing"})
	if _, err = r.Invoke(context.Background(), "managed_resource_plan", args, "test"); err != nil {
		t.Fatal(err)
	}
}
