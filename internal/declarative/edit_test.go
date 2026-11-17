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

func TestServiceEditWithCustomMountPaths(t *testing.T) {
	c, cfg, _ := coordinatorFixture(t)
	snap, err := c.Store.Save(cfg, "missing")
	if err != nil {
		t.Fatal(err)
	}
	serviceJSON := `{
		"kind": "container",
		"engine": "podman",
		"backend": "compose",
		"enabled": false,
		"containers": {
			"web": {
				"image": "docker.io/library/nginx:alpine",
				"mounts": [
					{"path": "/srv/app/data", "kind": "directory", "target": "/data", "readOnly": false},
					{"path": "/etc/ssl/certs/cert.pem", "kind": "file", "target": "/etc/ssl/cert.pem", "readOnly": true}
				]
			}
		}
	}`
	updated, err := c.EditResource("service", "webapp", "save", serviceJSON, snap.Revision)
	if err != nil {
		t.Fatal(err)
	}
	svc, ok := updated.Services["webapp"]
	if !ok {
		t.Fatal("webapp service not found")
	}
	web, ok := svc.Containers["web"]
	if !ok || len(web.Mounts) != 2 {
		t.Fatalf("expected 2 mounts in web container, got %+v", web.Mounts)
	}
	dirMount := web.Mounts[0]
	fileMount := web.Mounts[1]
	if dirMount.Storage == "" || fileMount.Storage == "" {
		t.Fatalf("expected derived storage IDs, got dir=%q, file=%q", dirMount.Storage, fileMount.Storage)
	}
	dirStorage, ok := updated.Storage[dirMount.Storage]
	if !ok || dirStorage.Path != "/srv/app/data" || dirStorage.Kind != "directory" || !dirStorage.External {
		t.Fatalf("unexpected dir storage: %+v", dirStorage)
	}
	fileStorage, ok := updated.Storage[fileMount.Storage]
	if !ok || fileStorage.Path != "/etc/ssl/certs/cert.pem" || fileStorage.Kind != "file" || !fileStorage.External {
		t.Fatalf("unexpected file storage: %+v", fileStorage)
	}
}

