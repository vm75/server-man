package declarative

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const example = `version: 1
storage:
  content:
    path: data/content
services:
  website:
    kind: container
    enabled: true
    containers:
      web:
        image: docker.io/library/nginx:stable
        mounts:
          - storage: content
            target: /srv/content
`

func TestParseDefaultsAndRoundTrip(t *testing.T) {
	c, err := Parse([]byte(example))
	if err != nil {
		t.Fatal(err)
	}
	s := c.Services["website"]
	if s.Engine != "podman" || s.Backend != "quadlet" {
		t.Fatalf("defaults: %+v", s)
	}
	a, err := Encode(c)
	if err != nil {
		t.Fatal(err)
	}
	d, err := Parse(a)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Encode(d)
	if err != nil || string(a) != string(b) {
		t.Fatal("serialization is not deterministic", err)
	}
}
func TestRejectInvalidDefinitions(t *testing.T) {
	cases := []string{
		"version: 1\nversion: 1\n", "version: 2\n", "version: 1\nunknown: private-value\n", "version: 1\n---\nversion: 1\n", "version: &v 1\n", "version: !!int 1\n", "version: 1\nservices: wrong\n",
		strings.Replace(example, "data/content", "../escape", 1),
		strings.Replace(example, "storage: content", "storage: absent", 1),
		strings.Replace(example, "kind: container", "kind: container\n    engine: docker\n    backend: quadlet", 1),
		strings.Replace(example, "enabled: true", "enabled: true\n    depends_on: [website]", 1),
		strings.Replace(example, "enabled: true", "enabled: true\n    depends_on: [absent]", 1),
	}
	for i, input := range cases {
		if _, err := Parse([]byte(input)); err == nil {
			t.Errorf("case %d accepted", i)
		} else if strings.Contains(err.Error(), "private-value") {
			t.Fatal("diagnostic exposed value")
		}
	}
	if _, err := Parse([]byte(strings.Repeat("x", MaxConfigBytes+1))); err == nil {
		t.Fatal("oversize accepted")
	}
}
func TestStoreRevisionAndPrivateFiles(t *testing.T) {
	dir := t.TempDir()
	s := Store{Path: filepath.Join(dir, "server-man.yaml")}
	c, err := Parse([]byte(example))
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.Save(c, "missing")
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Read()
	if err != nil || got.Revision != first.Revision {
		t.Fatal(got, err)
	}
	if _, err = s.Save(c, "missing"); !errors.Is(err, ErrConflict) {
		t.Fatal("stale create", err)
	}
	before, _ := os.ReadFile(s.Path)
	bad := c
	bad.Version = 2
	if _, err = s.Save(bad, first.Revision); err == nil {
		t.Fatal("invalid save succeeded")
	}
	after, _ := os.ReadFile(s.Path)
	if string(before) != string(after) {
		t.Fatal("invalid save changed file")
	}
	if s.Resolve("data/content") != filepath.Join(dir, "data/content") {
		t.Fatal("wrong path base")
	}
	if err = os.Chmod(s.Path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Read(); err == nil {
		t.Fatal("public config accepted")
	}
}
func TestStoreRejectsSymlinks(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte(example), 0600); err != nil {
		t.Fatal(err)
	}
	s := Store{Path: filepath.Join(dir, "config")}
	if err := os.Symlink(target, s.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Read(); err == nil {
		t.Fatal("symlink accepted")
	}
}
