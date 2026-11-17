package declarative

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPublicationManifestTracksOwnedFilesOutsideGeneratorDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "generated", "application")
	old := map[string][]byte{"service.container": []byte(ownedHeader + "old\n")}
	newFiles := map[string][]byte{"service.container": []byte(ownedHeader + "new\n")}
	if err := publishArtifacts(dir, nil, old); err != nil {
		t.Fatal(err)
	}
	m, _, err := readArtifactManifest(dir)
	if err != nil || m.Owner != "server-man" || m.Version != 1 || m.Files["service.container"] != digest(old["service.container"]) {
		t.Fatal("missing private checksummed publication", m, err)
	}
	info, err := os.Lstat(artifactManifestPath(dir))
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		t.Fatal("publication manifest is not private", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "service.container" {
		t.Fatal("manifest was placed inside the native generator directory", err, entries)
	}
	if err := publishArtifacts(dir, old, newFiles); err != nil {
		t.Fatal("normal revision update failed", err)
	}
	m, _, err = readArtifactManifest(dir)
	if err != nil || m.Files["service.container"] != digest(newFiles["service.container"]) {
		t.Fatal("manifest did not advance with publication", m, err)
	}
	// Simulate a crash after the native file replacement but before its
	// checksum inventory advanced: explicit retry accepts only exact next bytes.
	if err = os.WriteFile(artifactManifestPath(dir), []byte("{\"owner\":\"server-man\",\"version\":1,\"files\":{\"service.container\":\""+digest(old["service.container"])+"\"}}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := publishArtifacts(dir, old, newFiles); err != nil {
		t.Fatal("interrupted publication could not reconcile", err)
	}
	m, _, err = readArtifactManifest(dir)
	if err != nil || m.Files["service.container"] != digest(newFiles["service.container"]) {
		t.Fatal("manifest not recovered after explicit retry", m, err)
	}
	if err := publishArtifacts(dir, newFiles, nil); err != nil {
		t.Fatal("definition-only removal failed", err)
	}
	if _, err := os.Stat(artifactManifestPath(dir)); !os.IsNotExist(err) {
		t.Fatal("stale publication manifest retained", err)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatal("native generated data retained after owned removal", err, entries)
	}
}

func TestPublicationManifestConflictsPreserveUnmanagedData(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "application")
	files := map[string][]byte{"unit.container": []byte(ownedHeader + "payload\n")}
	if err := publishArtifacts(dir, nil, files); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(parent, "unmanaged")
	if err := os.WriteFile(sentinel, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	path := artifactManifestPath(dir)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sentinel, path); err != nil {
		t.Fatal(err)
	}
	if err := publishArtifacts(dir, files, map[string][]byte{"unit.container": []byte(ownedHeader + "changed\n")}); !errors.Is(err, errArtifactConflict) {
		t.Fatalf("manifest symlink was overwritten: %v", err)
	}
	if got, err := os.ReadFile(sentinel); err != nil || string(got) != "keep" {
		t.Fatal("foreign file changed", err)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "unit.container")); err != nil || string(got) != string(files["unit.container"]) {
		t.Fatal("native artifact changed despite manifest conflict", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := publishArtifacts(dir, files, files); !errors.Is(err, errArtifactConflict) {
		t.Fatalf("corrupt publication inventory accepted: %v", err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if err := publishArtifacts(dir, files, files); !errors.Is(err, errArtifactConflict) {
		t.Fatalf("public publication inventory accepted: %v", err)
	}
}
