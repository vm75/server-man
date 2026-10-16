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
)

type Config struct {
	Listen       string `json:"listen"`
	VMRoot       string `json:"vmRoot"`
	StateDir     string `json:"stateDir"`
	TokenFile    string `json:"tokenFile"`
	PublicOrigin string `json:"publicOrigin"`
	AllowWrites  bool   `json:"allowWrites"`
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
	return Config{Listen: "127.0.0.1:8080", VMRoot: filepath.Join(home, "vms"), StateDir: filepath.Join(stateRoot, "serverman"), TokenFile: filepath.Join(configDir, "serverman", "token")}, nil
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
	if !filepath.IsAbs(c.StateDir) || !filepath.IsAbs(c.TokenFile) {
		return errors.New("stateDir and tokenFile must be absolute paths")
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
