package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPackagedExampleConfig(t *testing.T) {
	if _, err := Load("../../packaging/server-man.example.json"); err != nil {
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
		{"absolute manifest", `{"manifestPath":"/srv/workloads/server-man.yaml"}`, true},
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
		{"custom Samba paths", `{"sambaConfig":"/srv/samba/smb.conf","sambaManagedFile":"/srv/samba/serverman.conf"}`, true},
		{"relative Samba path", `{"sambaConfig":"smb.conf"}`, false},
		{"same Samba paths", `{"sambaConfig":"/srv/smb.conf","sambaManagedFile":"/srv/smb.conf"}`, false},
		{"Samba substitutions", `{"sambaManagedFile":"/srv/%U.conf"}`, false},
		{"Samba control character", `{"sambaManagedFile":"/srv/tab\tfile"}`, false},
		{"Samba NFS overlap", `{"sambaManagedFile":"/etc/exports.d/serverman.exports"}`, false},
		{"selected package manager", `{"packageManager":"dnf"}`, true},
		{"invalid package manager", `{"packageManager":"yum -y"}`, false},
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
