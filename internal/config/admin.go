package config

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"go.yaml.in/yaml/v3"
)

const AdminPasswordIterations = 600000

var adminUsernamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,63}$`)

var ErrAdminConfigured = errors.New("administrator already configured")
var ErrSetupConflict = errors.New("configuration changed during administrator setup")

func ValidAdminUsername(name string) bool { return adminUsernamePattern.MatchString(name) }

// Password hashes have one fixed, bounded PBKDF2-SHA256 format. No raw password
// is accepted in YAML, and iteration counts cannot be supplied by callers.
func ValidAdminPasswordHash(value string) bool {
	parts := strings.Split(value, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" || parts[1] != "600000" {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil || len(salt) != 16 {
		return false
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[3])
	return err == nil && len(key) == 32
}

// SetupAdmin makes a one-time, revision-checked update to the same private YAML
// used by managed workloads. The caller holds the shared mutation.lock.
func SetupAdmin(path, username, passwordHash string) error {
	if !filepath.IsAbs(path) || !ValidAdminUsername(username) || !ValidAdminPasswordHash(passwordHash) {
		return errors.New("invalid administrator setup")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	before, err := readAdminConfiguration(path)
	missing := os.IsNotExist(err)
	if err != nil && !missing {
		return err
	}
	if missing {
		before = []byte("version: 1\n")
	}
	settings, err := ReadServer(before)
	if err != nil {
		return err
	}
	if settings != nil && settings.AdminUsername != "" {
		return ErrAdminConfigured
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(before, &doc); err != nil || len(doc.Content) != 1 {
		return errors.New("invalid configuration document")
	}
	root := doc.Content[0]
	var server *yaml.Node
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "server" {
			server = root.Content[i+1]
			break
		}
	}
	if server == nil {
		server = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "server"}, server)
	}
	if server.Kind == yaml.ScalarNode && server.Tag == "!!null" {
		*server = yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	}
	if server.Kind != yaml.MappingNode {
		return errors.New("server settings must be a mapping")
	}
	for _, field := range [][2]string{{"admin_username", username}, {"admin_password_hash", passwordHash}} {
		found := false
		for i := 0; i+1 < len(server.Content); i += 2 {
			if server.Content[i].Value == field[0] {
				server.Content[i+1] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: field[1]}
				found = true
				break
			}
		}
		if !found {
			server.Content = append(server.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: field[0]},
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: field[1]})
		}
	}
	after, err := yaml.Marshal(&doc)
	if err != nil {
		return errors.New("cannot encode administrator settings")
	}
	if len(after) > 1<<20 {
		return errors.New("configuration exceeds 1 MiB")
	}
	if _, err := ReadServer(after); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".server-man-admin-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(after)
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	latest, readErr := readAdminConfiguration(path)
	if missing {
		if !os.IsNotExist(readErr) {
			return ErrSetupConflict
		}
	} else if readErr != nil || !bytes.Equal(latest, before) {
		return ErrSetupConflict
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func readAdminConfiguration(path string) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("configuration must be an owner-only regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 1<<20 {
		return nil, errors.New("configuration exceeds 1 MiB")
	}
	return data, nil
}
