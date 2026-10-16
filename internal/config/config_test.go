package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadStrictConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"defaults", `{}`, true},
		{"ipv6", `{"listen":"[::1]:8081","vmRoot":"/srv/vms"}`, true},
		{"unknown", `{"writse":true}`, false},
		{"relative root", `{"vmRoot":"vms"}`, false},
		{"remote without authentication", `{"listen":":8080"}`, false},
		{"trailing object", `{} {}`, false},
		{"null", `null`, false},
		{"invalid port", `{"listen":"127.0.0.1:70000"}`, false},
		{"relative state", `{"stateDir":"state"}`, false},
		{"public origin", `{"publicOrigin":"https://server.example.com"}`, true},
		{"insecure origin", `{"publicOrigin":"http://server.example.com"}`, false},
		{"origin with path", `{"publicOrigin":"https://server.example.com/path"}`, false},
		{"sample interval", `{"sampleIntervalSeconds":5}`, true},
		{"sample interval too small", `{"sampleIntervalSeconds":0}`, false},
		{"sample interval too large", `{"sampleIntervalSeconds":11}`, false},
		{"custom Compose roots", `{"podmanRoot":"/srv/podman","dockerRoot":"/srv/docker"}`, true},
		{"relative Compose root", `{"podmanRoot":"config/podman"}`, false},
		{"machine contexts", `{"lxdRemote":"lab","lxdProject":"workloads","lxcPath":"/srv/lxc","libvirtURI":"qemu:///session","libvirtXMLRoot":"/srv/xml"}`, true},
		{"unsafe LXD remote", `{"lxdRemote":"--debug"}`, false},
		{"LXD target in context", `{"lxdRemote":"remote:guest"}`, false},
		{"empty LXD project", `{"lxdProject":""}`, false},
		{"relative LXC path", `{"lxcPath":"containers"}`, false},
		{"remote libvirt", `{"libvirtURI":"qemu+ssh://root@example/system"}`, false},
		{"relative XML root", `{"libvirtXMLRoot":"xml"}`, false},
		{"user services", `{"systemdScope":"user"}`, true},
		{"invalid service scope", `{"systemdScope":"global"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(file, []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(file)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
		})
	}
}
