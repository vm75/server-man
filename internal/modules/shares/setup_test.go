package shares

import (
	"context"
	"errors"
	"os"
	"serverman/internal/core"
	"strings"
	"testing"
)

func TestPreparePreservesConfigurationAndIsIdempotent(t *testing.T) {
	b, _, _ := fixture(t)
	original := []byte("# administrator settings\n[global]\n workgroup = OFFICE\n[existing]\n path = /srv/existing\n")
	if err := os.WriteFile(b.sambaConfig, original, 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(b.sambaConfig, 0640); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := b.prepare(context.Background(), "samba"); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(b.sambaConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(got), string(original)) || strings.Count(string(got), "include = "+b.sambaManaged) != 1 {
		t.Fatal(string(got))
	}
	info, _ := os.Stat(b.sambaConfig)
	if info.Mode().Perm() != 0640 {
		t.Fatal(info.Mode())
	}
}
func TestPrepareValidationFailurePreservesMain(t *testing.T) {
	b, r, _ := fixture(t)
	original := []byte("[global]\n workgroup = OFFICE\n")
	if err := os.WriteFile(b.sambaConfig, original, 0644); err != nil {
		t.Fatal(err)
	}
	r.captureOverride = func(core.Command) (string, error) { return "", errors.New("invalid configuration") }
	if err := b.prepare(context.Background(), "samba"); err == nil {
		t.Fatal("expected validation failure")
	}
	got, _ := os.ReadFile(b.sambaConfig)
	if string(got) != string(original) {
		t.Fatal("main file changed")
	}
}
func TestPrepareNFSPreservesNativeExports(t *testing.T) {
	b, _, _ := fixture(t)
	if err := os.WriteFile(b.nfsExports, []byte("/srv/existing 192.0.2.1(ro)\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := b.prepare(context.Background(), "nfs"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(b.nfsExports)
	if string(got) != "/srv/existing 192.0.2.1(ro)\n" {
		t.Fatal("native exports changed")
	}
}
