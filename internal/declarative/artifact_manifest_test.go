package declarative

import (
	"bytes"
	"encoding/json"
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

func TestPublicationRejectsForeignWellFormedManifest(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "resource")
	old := map[string][]byte{"service.container": []byte(ownedHeader + "old\n")}
	next := map[string][]byte{"service.container": []byte(ownedHeader + "next\n")}
	if err := publishArtifacts(dir, nil, old); err != nil {
		t.Fatal(err)
	}
	path := artifactManifestPath(dir)
	prior, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, altered := range []map[string][]byte{
		{"service.container": []byte(ownedHeader + "foreign\n")},
		{"service.container": old["service.container"], "extra.container": []byte(ownedHeader + "extra\n")},
	} {
		fake, err := json.Marshal(artifactManifestFor(altered))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, fake, 0600); err != nil {
			t.Fatal(err)
		}
		if err := publishArtifacts(dir, old, next); !errors.Is(err, errArtifactConflict) {
			t.Fatalf("well-formed foreign manifest accepted: %v", err)
		}
		if actual, err := os.ReadFile(filepath.Join(dir, "service.container")); err != nil || string(actual) != string(old["service.container"]) {
			t.Fatal("manifest conflict overwrote existing generated content", err)
		}
		if actual, err := os.ReadFile(path); err != nil || string(actual) != string(fake) {
			t.Fatal("manifest conflict overwrote outside-session inventory", err)
		}
	}
	if err := os.WriteFile(path, prior, 0600); err != nil {
		t.Fatal(err)
	}
	if err := publishArtifacts(dir, old, next); err != nil {
		t.Fatal("valid previous manifest blocked normal publication", err)
	}
	if err := publishArtifacts(dir, old, next); err != nil {
		t.Fatal("desired manifest blocked interrupted retry", err)
	}
}

func TestPublicationManifestRejectsAmbiguousJSON(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "application")
	files := map[string][]byte{"service.container": []byte(ownedHeader + "original\n")}
	if err := publishArtifacts(dir, nil, files); err != nil {
		t.Fatal(err)
	}
	path := artifactManifestPath(dir)
	canonical, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	duplicateOwner := bytes.Replace(canonical, []byte(`"owner":"server-man"`), []byte(`"owner":"server-man","owner":"server-man"`), 1)
	duplicateFile := bytes.Replace(canonical, []byte(`"service.container":`), []byte(`"service.container":"`+digest(files["service.container"])+`","service.container":`), 1)
	for _, altered := range [][]byte{
		append([]byte{' '}, canonical...),
		append(append([]byte{}, canonical...), '\n'),
		duplicateOwner,
		duplicateFile,
	} {
		if bytes.Equal(altered, canonical) {
			t.Fatal("test did not alter manifest bytes")
		}
		if err := os.WriteFile(path, altered, 0600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := readArtifactManifest(dir); err == nil {
			t.Fatal("noncanonical publication manifest accepted")
		}
		if err := publishArtifacts(dir, files, files); !errors.Is(err, errArtifactConflict) {
			t.Fatal("noncanonical sidecar permitted publication", err)
		}
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, altered) {
			t.Fatal("rejected sidecar was overwritten", err)
		}
	}
	if err := os.WriteFile(path, canonical, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readArtifactManifest(dir); err != nil {
		t.Fatal("canonical sidecar incorrectly rejected", err)
	}
}
