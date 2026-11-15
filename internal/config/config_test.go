package config

import (
	"encoding/json"
	"go.yaml.in/yaml/v3"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackagedExampleConfig(t *testing.T) {
	data, err := os.ReadFile("../../packaging/server-man.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "server-man.yaml")
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("packaged example config is invalid: %v", err)
	}
}

func TestLoadStrictConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"defaults", `{}`, true},
		{"privileged mode", `{"privilegedOperations":true}`, true},
		{"helper paths fixed", `{"privilegedOperations":true,"sambaConfig":"/tmp/smb.conf"}`, false},
		{"helper native packages", `{"privilegedOperations":true,"packageManager":"apt"}`, false},
		{"ipv6", `{"listen":"[::1]:8081"}`, true},
		{"relative manifest", `{"manifestPath":"workloads.yaml"}`, false},
		{"removed manifest", `{"manifestPath":"/srv/workloads/server-man.yaml"}`, false},
		{"unknown", `{"writse":true}`, false},
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
		{"remote libvirt", `{"libvirtURI":"qemu+ssh://root@example/system"}`, false},
		{"local libvirt", `{"libvirtURI":"qemu:///session"}`, true},
		{"removed discovery setting", `{"vmRoot":"/srv/vms"}`, false},
		{"removed LXD setting", `{"lxdRemote":"local"}`, false},
		{"user services", `{"systemdScope":"user"}`, true},
		{"invalid service scope", `{"systemdScope":"global"}`, false},
		{"custom Samba paths", `{"sambaConfig":"/srv/samba/smb.conf","sambaManagedFile":"/srv/samba/server-man.conf"}`, true},
		{"relative Samba path", `{"sambaConfig":"smb.conf"}`, false},
		{"same Samba paths", `{"sambaConfig":"/srv/smb.conf","sambaManagedFile":"/srv/smb.conf"}`, false},
		{"Samba substitutions", `{"sambaManagedFile":"/srv/%U.conf"}`, false},
		{"Samba control character", `{"sambaManagedFile":"/srv/tab\tfile"}`, false},
		{"Samba NFS overlap", `{"sambaManagedFile":"/etc/exports.d/server-man.exports"}`, false},
		{"selected package manager", `{"packageManager":"dnf"}`, true},
		{"invalid package manager", `{"packageManager":"yum -y"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "server-man.yaml")
			body := []byte(tc.body)
			var server map[string]any
			if tc.body != "null" && json.Unmarshal(body, &server) == nil {
				keys := map[string]string{"libvirtURI": "libvirt_uri", "systemdScope": "systemd_scope", "sambaConfig": "samba_config", "sambaManagedFile": "samba_managed_file", "packageManager": "package_manager", "stateDir": "state_dir", "tokenFile": "token_file", "publicOrigin": "public_origin", "privilegedOperations": "privileged_operations", "allowWrites": "allow_writes", "sampleIntervalSeconds": "sample_interval_seconds"}
				for old, next := range keys {
					if value, ok := server[old]; ok {
						server[next] = value
						delete(server, old)
					}
				}
				body, _ = yaml.Marshal(map[string]any{"version": 1, "server": server})
			}
			if err := os.WriteFile(file, body, 0600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(file)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
		})
	}
}

func TestUnifiedStructureRejectsAmbiguousYAML(t *testing.T) {
	for _, body := range []string{"version: 1\nserver: {allow_writes: false, allow_writes: true}", "version: 1\nserver: &x {listen: '127.0.0.1:8080'}", "version: 1\nserver: {password: private-value}", "version: 1\nserver: {listen: private-value}", "version: 1\n---\nversion: 1", "version: 1\nmanifestPath: /other"} {
		_, err := ReadServer([]byte(body))
		if err == nil || strings.Contains(err.Error(), "private-value") {
			t.Fatalf("unsafe or unredacted result: %v", err)
		}
	}
}

func TestDefaultUnifiedPathAndResourceRepair(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	cfg, err := Load("")
	if err != nil || cfg.Path != filepath.Join(dir, "server-man/server-man.yaml") {
		t.Fatal(cfg.Path, err)
	}
	file := filepath.Join(t.TempDir(), "server-man.yaml")
	if err := os.WriteFile(file, []byte("version: 1\nserver: {listen: '127.0.0.1:8181'}\nservices: {invalid: {unknown: repair-me}}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(file)
	if err != nil || cfg.Listen != "127.0.0.1:8181" || cfg.Path != file {
		t.Fatal(cfg, err)
	}
	if err := os.Chmod(file, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = Load(file); err == nil {
		t.Fatal("public configuration accepted")
	}
}
