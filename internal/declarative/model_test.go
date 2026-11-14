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

const vmExample = `version: 1
bindings:
  lan:
    kind: network
    name: default
services:
  guest:
    kind: vm
    backend: libvirt
    enabled: false
    vm:
      image: assets/guest.qcow2
      cpus: 2
      memory_mib: 1024
      network: lan
`

func TestStableIdentityAndHistory(t *testing.T) {
	s := Store{Path: filepath.Join(t.TempDir(), "config.yaml")}
	c, err := Parse([]byte(vmExample))
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.Save(c, "missing")
	if err != nil {
		t.Fatal(err)
	}
	identity := first.Config.Services["guest"].VM
	if identity.UUID == "" || identity.MAC == "" {
		t.Fatal("missing identity")
	}
	revision := first.Revision
	for i := 0; i < HistoryLimit+3; i++ {
		svc := c.Services["guest"]
		svc.Description = strings.Repeat("a", i+1)
		c.Services["guest"] = svc
		next, err := s.Save(c, revision)
		if err != nil {
			t.Fatal(err)
		}
		revision = next.Revision
		v := next.Config.Services["guest"].VM
		if v.UUID != identity.UUID || v.MAC != identity.MAC {
			t.Fatal("identity changed")
		}
	}
	entries, err := os.ReadDir(s.Path + ".history")
	if err != nil || len(entries) != HistoryLimit {
		t.Fatal("history not bounded", len(entries), err)
	}
	if c.Services["guest"].VM.UUID != "" {
		t.Fatal("caller config mutated")
	}
}
func TestIdentityChangeRejected(t *testing.T) {
	c, err := Parse([]byte(vmExample))
	if err != nil {
		t.Fatal(err)
	}
	first, err := PrepareIdentities(c, Config{})
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := Parse([]byte(vmExample))
	if err != nil {
		t.Fatal(err)
	}
	candidate.Services["guest"].VM.UUID = "00000000-0000-4000-8000-000000000001"
	if _, err = PrepareIdentities(candidate, first); err == nil {
		t.Fatal("identity edit accepted")
	}
}
func TestRejectNativeInjectionAndInvalidAccess(t *testing.T) {
	cases := []string{
		strings.Replace(example, "image: docker.io/library/nginx:stable", "image: '-invalid'", 1),
		strings.Replace(example, "image: docker.io/library/nginx:stable", "image: nginx\n        ports: ['70000:80']", 1),
		example + "shares:\n  documents:\n    protocol: samba\n    storage: content\n    users: ['alice\\nadmin']\n",
		strings.Replace(vmExample, "kind: network", "kind: pool", 1),
		strings.Replace(vmExample, "cpus: 2", "cpus: 0", 1),
	}
	for i, input := range cases {
		if _, err := Parse([]byte(input)); err == nil {
			t.Errorf("invalid case %d accepted", i)
		}
	}
}

func TestPortedMissingDataBlocksDependantsWithoutCreation(t *testing.T) {
	c, err := Parse([]byte(example))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	s := Store{Path: filepath.Join(root, "config.yaml")}
	health := s.CheckFiles(c)
	seen := map[string]bool{}
	for _, i := range health.Issues {
		seen[i.Resource] = true
	}
	if !seen["storage/content"] || !seen["service/website"] {
		t.Fatal("missing dependent diagnostics", health)
	}
	if _, err = os.Stat(filepath.Join(root, "data/content")); !os.IsNotExist(err) {
		t.Fatal("preflight created missing data")
	}
	if err = os.MkdirAll(filepath.Join(root, "data/content"), 0700); err != nil {
		t.Fatal(err)
	}
	if got := s.CheckFiles(c); len(got.Issues) != 0 {
		t.Fatal(got)
	}
}
func TestPortableSymlinkEscapeAndUnverifiedMountBlock(t *testing.T) {
	c, err := Parse([]byte(example))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err = os.Symlink(t.TempDir(), filepath.Join(root, "data")); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(root, "data/content"), 0700); err != nil {
		t.Fatal(err)
	}
	s := Store{Path: filepath.Join(root, "config.yaml")}
	h := s.CheckFiles(c)
	found := false
	for _, i := range h.Issues {
		if i.Code == "path_escape" {
			found = true
		}
	}
	if !found {
		t.Fatal(h)
	}
	st := c.Storage["content"]
	st.Mount = "expected-filesystem"
	c.Storage["content"] = st
	h = s.CheckFiles(c)
	found = false
	for _, i := range h.Issues {
		if i.Code == "mount_check_pending" {
			found = true
		}
	}
	if !found {
		t.Fatal("unverified mount treated as ready")
	}
}
