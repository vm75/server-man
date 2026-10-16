// Package config reads the small, explicit server configuration.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

type Config struct {
	Listen                string `json:"listen"`
	VMRoot                string `json:"vmRoot"`
	PodmanRoot            string `json:"podmanRoot"`
	DockerRoot            string `json:"dockerRoot"`
	LXDRemote             string `json:"lxdRemote"`
	LXDProject            string `json:"lxdProject"`
	LXCPath               string `json:"lxcPath"`
	LibvirtURI            string `json:"libvirtURI"`
	LibvirtXMLRoot        string `json:"libvirtXMLRoot"`
	SystemdScope          string `json:"systemdScope"`
	SambaConfig           string `json:"sambaConfig"`
	SambaManagedFile      string `json:"sambaManagedFile"`
	PackageManager        string `json:"packageManager"`
	StateDir              string `json:"stateDir"`
	TokenFile             string `json:"tokenFile"`
	PublicOrigin          string `json:"publicOrigin"`
	AllowWrites           bool   `json:"allowWrites"`
	SampleIntervalSeconds int    `json:"sampleIntervalSeconds"`
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
	return Config{Listen: "127.0.0.1:8080", VMRoot: filepath.Join(home, "vms"), StateDir: filepath.Join(stateRoot, "serverman"), TokenFile: filepath.Join(configDir, "serverman", "token"), LXDRemote: "local", LXDProject: "default", LibvirtURI: "qemu:///system", SystemdScope: "system", SambaConfig: "/etc/samba/smb.conf", SambaManagedFile: "/etc/samba/serverman.conf", PackageManager: "auto", SampleIntervalSeconds: 2}, nil
}

func Load(path string) (Config, error) {
	cfg, err := Defaults()
	if err != nil {
		return cfg, err
	}
	if path != "" {
		file, err := os.Open(path)
		if err != nil {
			return cfg, fmt.Errorf("open config: %w", err)
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
		if err != nil {
			return cfg, fmt.Errorf("read config: %w", err)
		}
		if len(data) > 64<<10 {
			return cfg, errors.New("config exceeds 64 KiB")
		}
		if data = bytes.TrimSpace(data); len(data) == 0 || data[0] != '{' {
			return cfg, errors.New("config must be a JSON object")
		}
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&cfg); err != nil {
			return cfg, fmt.Errorf("decode config: %w", err)
		}
		if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
			return cfg, errors.New("config must contain one JSON object")
		}
	}
	return cfg, cfg.Validate()
}

func (c Config) Validate() error {
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
	// Use a TLS reverse proxy for remote access; the management socket stays local.
	if host != "localhost" && (net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback()) {
		return errors.New("listen must be a loopback address")
	}
	if !filepath.IsAbs(c.VMRoot) {
		return errors.New("vmRoot must be an absolute path")
	}
	if (c.PodmanRoot != "" && !filepath.IsAbs(c.PodmanRoot)) || (c.DockerRoot != "" && !filepath.IsAbs(c.DockerRoot)) {
		return errors.New("podmanRoot and dockerRoot must be absolute when configured")
	}
	if !filepath.IsAbs(c.StateDir) || !filepath.IsAbs(c.TokenFile) {
		return errors.New("stateDir and tokenFile must be absolute paths")
	}
	for _, path := range []string{c.LXCPath, c.LibvirtXMLRoot} {
		if path != "" && !filepath.IsAbs(path) {
			return errors.New("lxcPath and libvirtXMLRoot must be absolute when configured")
		}
	}
	for _, value := range []string{c.LXDRemote, c.LXDProject} {
		if value == "" || len(value) > 128 || strings.ContainsAny(value, ":/\\ \t\r\n") || strings.HasPrefix(value, "-") {
			return errors.New("lxdRemote and lxdProject must be simple nonempty names")
		}
		for _, char := range value {
			if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || strings.ContainsRune("._-", char)) {
				return errors.New("lxdRemote and lxdProject contain unsupported characters")
			}
		}
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
		if path == "/etc/exports" || path == "/etc/exports.d/serverman.exports" {
			return errors.New("Samba config paths must not overlap native NFS configuration")
		}
	}
	if c.SambaConfig == c.SambaManagedFile {
		return errors.New("sambaManagedFile must differ from sambaConfig")
	}
	if c.PublicOrigin != "" {
		origin, err := url.Parse(c.PublicOrigin)
		if err != nil || origin.Scheme != "https" || origin.Host == "" || origin.User != nil || (origin.Path != "" && origin.Path != "/") || origin.RawQuery != "" || origin.Fragment != "" {
			return errors.New("publicOrigin must be an HTTPS origin, e.g. https://server.example.com")
		}
		if strings.HasSuffix(origin.Host, ":") {
			return errors.New("publicOrigin has an invalid port")
		}
	}
	return nil
}
