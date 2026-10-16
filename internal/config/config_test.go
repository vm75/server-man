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
