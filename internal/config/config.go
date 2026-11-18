// Package config reads server settings from the unified YAML configuration.
package config

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unicode"
)

type Config struct {
	Path                  string `json:"-" yaml:"-"`
	Listen                string `json:"listen" yaml:"listen,omitempty"`
	LibvirtURI            string `json:"libvirtURI" yaml:"libvirt_uri,omitempty"`
	SystemdScope          string `json:"systemdScope" yaml:"systemd_scope,omitempty"`
	SambaConfig           string `json:"sambaConfig" yaml:"samba_config,omitempty"`
	SambaManagedFile      string `json:"sambaManagedFile" yaml:"samba_managed_file,omitempty"`
	PackageManager        string `json:"packageManager" yaml:"package_manager,omitempty"`
	StateDir              string `json:"stateDir" yaml:"state_dir,omitempty"`
	TokenFile             string `json:"tokenFile" yaml:"token_file,omitempty"`
	AdminUsername         string `json:"adminUsername" yaml:"admin_username,omitempty"`
	AdminPasswordHash     string `json:"-" yaml:"admin_password_hash,omitempty"`
	PublicOrigin          string `json:"publicOrigin" yaml:"public_origin,omitempty"`
	PrivilegedOperations  bool   `json:"privilegedOperations" yaml:"privileged_operations,omitempty"`
	AllowWrites           bool   `json:"allowWrites" yaml:"allow_writes,omitempty"`
	SampleIntervalSeconds int    `json:"sampleIntervalSeconds" yaml:"sample_interval_seconds,omitempty"`
}

func Defaults() (Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Config{}, fmt.Errorf("find home directory: %w", err)
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return Config{}, err
	}
	stateRoot := os.Getenv("XDG_STATE_HOME")
	if stateRoot == "" {
		stateRoot = filepath.Join(home, ".local", "state")
	}
	return Config{Path: filepath.Join(configDir, "server-man", "server-man.yaml"), Listen: "127.0.0.1:8080", StateDir: filepath.Join(stateRoot, "server-man"), TokenFile: filepath.Join(configDir, "server-man", "token"), LibvirtURI: "qemu:///system", SystemdScope: "system", SambaConfig: "/etc/samba/smb.conf", SambaManagedFile: "/etc/samba/server-man.conf", PackageManager: "auto", SampleIntervalSeconds: 2}, nil
}

// Load selects one YAML document for both server settings and desired resources.
func Load(path string) (Config, error) {
	cfg, err := Defaults()
	if err != nil {
		return cfg, err
	}
	explicit := path != ""
	if !explicit {
		path = cfg.Path
	}
	if !filepath.IsAbs(path) {
		return cfg, errors.New("configuration path must be absolute")
	}
	cfg.Path = filepath.Clean(path)
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if os.IsNotExist(err) && !explicit {
		return cfg, cfg.Validate()
	}
	if err != nil {
		return cfg, fmt.Errorf("open configuration: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return cfg, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return cfg, errors.New("configuration must be an owner-only regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil {
		return cfg, err
	}
	settings, err := ReadServer(data)
	if err != nil {
		return cfg, err
	}
	if settings != nil {
		settings.Path = cfg.Path
		cfg = *settings
	}
	return cfg, cfg.Validate()
}

func (c Config) Validate() error {
	if (c.AdminUsername == "") != (c.AdminPasswordHash == "") {
		return errors.New("admin_username and admin_password_hash must be configured together")
	}
	if c.AdminUsername != "" && (!ValidAdminUsername(c.AdminUsername) || !ValidAdminPasswordHash(c.AdminPasswordHash)) {
		return errors.New("invalid admin username or password hash")
	}
	if c.Path != "" && !filepath.IsAbs(c.Path) {
		return errors.New("configuration path must be absolute")
	}
	if c.PrivilegedOperations && (c.SambaConfig != "/etc/samba/smb.conf" || c.SambaManagedFile != "/etc/samba/server-man.conf" || c.PackageManager != "auto") {
		return errors.New("privilegedOperations uses fixed native Samba paths and automatic package-manager detection")
	}

	if c.PackageManager != "auto" && c.PackageManager != "apt" && c.PackageManager != "dnf" && c.PackageManager != "pacman" && c.PackageManager != "zypper" {
		return errors.New("packageManager must be auto, apt, dnf, pacman or zypper")
	}
	if c.SampleIntervalSeconds < 1 || c.SampleIntervalSeconds > 10 {
		return errors.New("sampleIntervalSeconds must be between 1 and 10")
	}
	host, port, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return fmt.Errorf("listen must be host:port: %w", err)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return errors.New("listen port must be between 1 and 65535")
	}
	// Remote listening is an explicit opt-in: bind the IPv4 wildcard and pin
	// the accepted Host/Origin and port with publicOrigin.
	wildcard := host == "0.0.0.0"
	if host != "localhost" && !wildcard {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return errors.New("listen must be loopback or 0.0.0.0 with a configured publicOrigin")
		}
	}
	if wildcard && c.PublicOrigin == "" {
		return errors.New("0.0.0.0 requires publicOrigin to restrict accepted Host and Origin")
	}
	if !filepath.IsAbs(c.StateDir) || !filepath.IsAbs(c.TokenFile) {
		return errors.New("stateDir and tokenFile must be absolute paths")
	}
	if c.LibvirtURI != "qemu:///system" && c.LibvirtURI != "qemu:///session" {
		return errors.New("libvirtURI must be qemu:///system or qemu:///session")
	}
	if c.SystemdScope != "system" && c.SystemdScope != "user" {
		return errors.New("systemdScope must be system or user")
	}
	for _, path := range []string{c.SambaConfig, c.SambaManagedFile} {
		if !filepath.IsAbs(path) || strings.ContainsAny(path, "\\%") || strings.ContainsFunc(path, unicode.IsControl) || strings.TrimSpace(path) != path || filepath.Clean(path) != path {
			return errors.New("Samba config paths must be clean absolute literal paths")
		}
		if path == "/etc/exports" || path == "/etc/exports.d/server-man.exports" {
			return errors.New("Samba config paths must not overlap native NFS configuration")
		}
	}
	if c.SambaConfig == c.SambaManagedFile {
		return errors.New("sambaManagedFile must differ from sambaConfig")
	}
	if c.PublicOrigin != "" {
		origin, err := url.Parse(c.PublicOrigin)
		if err != nil || (origin.Scheme != "https" && origin.Scheme != "http") || origin.Host == "" || origin.User != nil || (origin.Path != "" && origin.Path != "/") || origin.RawQuery != "" || origin.Fragment != "" {
			return errors.New("publicOrigin must be an HTTP(S) origin without a path, query or fragment")
		}
		if origin.Scheme == "http" {
			originPort, portErr := strconv.Atoi(origin.Port())
			if portErr != nil || originPort != portNumber || !wildcard {
				return errors.New("HTTP publicOrigin requires a 0.0.0.0 listener and the same port")
			}
			ip := net.ParseIP(origin.Hostname())
			if ip == nil || !ip.IsGlobalUnicast() || ip.IsLoopback() {
				return errors.New("HTTP publicOrigin must use the server's non-loopback IP address")
			}
		}
		if strings.HasSuffix(origin.Host, ":") {
			return errors.New("publicOrigin has an invalid port")
		}
	}
	return nil
}
