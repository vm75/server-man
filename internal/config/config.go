// Package config reads the small, explicit server configuration.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
)

type Config struct {
	Listen string `json:"listen"`
	VMRoot string `json:"vmRoot"`
}

func Defaults() (Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Config{}, fmt.Errorf("find home directory: %w", err)
	}
	return Config{Listen: "127.0.0.1:8080", VMRoot: filepath.Join(home, "vms")}, nil
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
		dec := json.NewDecoder(io.LimitReader(file, 64<<10))
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
	host, _, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return fmt.Errorf("listen must be host:port: %w", err)
	}
	// Remote deployment is enabled once authentication is installed.
	if host != "localhost" && (net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback()) {
		return errors.New("listen must be a loopback address")
	}
	if !filepath.IsAbs(c.VMRoot) {
		return errors.New("vmRoot must be an absolute path")
	}
	return nil
}
