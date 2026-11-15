package declarative

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"server-man/internal/core"
)

func TestInspectionPolicyAndMalformedRecovery(t *testing.T) {
	root := t.TempDir()
	store := Store{Path: filepath.Join(root, "config.yaml")}
	malformed := "version: 1\nservices: [broken\n"
	if err := os.WriteFile(store.Path, []byte(malformed), 0600); err != nil {
		t.Fatal(err)
	}
	for _, writes := range []bool{false, true} {
		r := core.NewRegistry(writes, nil)
		if err := RegisterInspection(r, store); err != nil {
			t.Fatal(err)
		}
		raw, err := r.Invoke(context.Background(), "managed_config_read", json.RawMessage(`{}`), "test")
		if !writes {
			if err == nil {
				t.Fatal("private read bypassed policy")
			}
		} else {
			if err != nil {
				t.Fatal(err)
			}
			got := raw.(map[string]any)
			if got["valid"] != false || got["content"] != malformed {
				t.Fatal(got)
			}
		}
		health, err := r.Invoke(context.Background(), "managed_config_health", json.RawMessage(`{}`), "test")
		if err != nil {
			t.Fatal(err)
		}
		if health.(map[string]any)["valid"] != false {
			t.Fatal(health)
		}
		input, _ := json.Marshal(map[string]any{"content": example})
		result, err := r.Invoke(context.Background(), "managed_config_validate", input, "test")
		if err != nil {
			t.Fatal(err)
		}
		if result.(map[string]any)["runtimeChecked"] != false {
			t.Fatal("false runtime claim")
		}
	}
	raw, revision, err := store.ReadRaw()
	if err != nil || string(raw) != malformed {
		t.Fatal(err)
	}
	c, err := Parse([]byte(example))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Save(c, revision); err != nil {
		t.Fatal("repair failed", err)
	}
	preserved, err := os.ReadFile(filepath.Join(store.Path+".history", revision+".yaml"))
	if err != nil || string(preserved) != malformed {
		t.Fatal("repair lost original bytes", err)
	}
}
func TestHistoryStoresExactBytesAndRejectsUnsafeDirectory(t *testing.T) {
	store := Store{Path: filepath.Join(t.TempDir(), "config.yaml")}
	original := "# preserved original formatting\n" + example
	if err := os.WriteFile(store.Path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	old, err := store.Read()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Save(old.Config, old.Revision); err != nil {
		t.Fatal(err)
	}
	bytes, err := os.ReadFile(filepath.Join(store.Path+".history", old.Revision+".yaml"))
	if err != nil || digest(bytes) != old.Revision {
		t.Fatal("history hash does not match bytes", err)
	}
	if err = os.Chmod(store.Path+".history", 0755); err != nil {
		t.Fatal(err)
	}
	next, err := store.Read()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Save(next.Config, next.Revision); err == nil || !strings.Contains(err.Error(), "history") {
		t.Fatal("unsafe history accepted", err)
	}
}

func TestCoordinatorRegistryPolicyAndPreview(t *testing.T) {
	c, _, _ := coordinatorFixture(t)
	r := core.NewRegistry(false, nil)
	if err := RegisterCoordinator(r, c); err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]any{"content": example, "revision": "missing"})
	result, err := r.Invoke(context.Background(), "managed_config_plan", input, "test")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.(map[string]any)["plan"].(Plan).Changes) != 1 {
		t.Fatal(result)
	}
	input, _ = json.Marshal(map[string]any{"content": example, "revision": "missing", "mode": "now", "confirm": true})
	if _, err = r.Invoke(context.Background(), "managed_config_save", input, "test"); err == nil {
		t.Fatal("write disabled bypass")
	}
	if _, err = os.Stat(c.Store.Path); !os.IsNotExist(err) {
		t.Fatal("preview wrote manifest")
	}
}
