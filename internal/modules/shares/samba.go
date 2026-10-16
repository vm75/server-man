package shares

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"serverman/internal/core"
)

func shareKey(name string) string { return strings.ToLower(strings.Join(strings.Fields(name), "")) }

// Only a literal include of the owned fragment in the configured main file is
// supported. All other lines are copied byte-for-byte into a temporary shadow.
func (b *Backend) shadow(data []byte, candidate string) ([]byte, error) {
	lines := strings.Split(string(data), "\n")
	count := 0
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if ok && shareKey(key) == "include" && strings.TrimSpace(value) == b.sambaManaged {
			count++
			lines[i] = key + "= " + candidate
		}
	}
	if count != 1 {
		return nil, errors.New("Add exactly one literal include = " + b.sambaManaged + " at the end of the main Samba config before managing shares")
	}
	return []byte(strings.Join(lines, "\n")), nil
}

func parseSamba(text string) ([]Definition, error) {
	defaults := Definition{ReadOnly: true, Browseable: true}
	current := &defaults
	result := []Definition{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			name := strings.TrimSpace(line[1 : len(line)-1])
			if shareKey(name) == "global" {
				current = &defaults
				continue
			}
			d := defaults
			d.Users = append([]string{}, defaults.Users...)
			d.Name = name
			result = append(result, d)
			current = &result[len(result)-1]
			if len(result) > 1024 {
				return nil, errors.New("Samba inventory exceeds 1024 shares")
			}
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, errors.New("unsupported Samba output")
		}
		key = shareKey(key)
		value = strings.TrimSpace(value)
		yes := func(v string) bool { return slices.Contains([]string{"yes", "true", "1"}, strings.ToLower(v)) }
		switch key {
		case "path":
			current.Path = value
		case "readonly":
			current.ReadOnly = yes(value)
		case "guestok":
			current.Guest = yes(value)
		case "browseable", "browsable":
			current.Browseable = yes(value)
		case "validusers":
			current.Users = tokens(value)
		}
	}
	return result, nil
}
func (b *Backend) sambaEffective(ctx context.Context, file string) ([]Definition, error) {
	text, err := b.runner.Capture(ctx, core.Command{Program: b.programs["testparm"], Args: []string{"--suppress-prompt", file}, Dir: filepath.Dir(b.sambaConfig), Timeout: 7 * time.Second})
	if err != nil {
		return nil, errors.New("Samba validation failed; inspect the configuration with testparm on the host")
	}
	return parseSamba(text)
}
func sameSamba(a, c Definition) bool {
	aUsers, cUsers := append([]string{}, a.Users...), append([]string{}, c.Users...)
	slices.Sort(aUsers)
	slices.Sort(cUsers)
	return a.Path == c.Path && a.ReadOnly == c.ReadOnly && a.Guest == c.Guest && a.Browseable == c.Browseable && slices.Equal(aUsers, cUsers)
}
func (b *Backend) validateSamba(ctx context.Context, defs, previous []Definition, candidate string) error {
	main, err := readFile(b.sambaConfig, false)
	if err != nil || !main.exists {
		return errors.New("Samba main config is inaccessible")
	}
	shadow, err := b.shadow(main.data, candidate)
	if err != nil {
		return err
	}
	file, err := temporary(filepath.Dir(b.sambaConfig), shadow, 0600)
	if err != nil {
		return err
	}
	defer os.Remove(file)
	empty, err := temporary(filepath.Dir(b.sambaManaged), []byte(header), 0600)
	if err != nil {
		return err
	}
	defer os.Remove(empty)
	unmanagedShadow, err := b.shadow(main.data, empty)
	if err != nil {
		return err
	}
	unmanagedFile, err := temporary(filepath.Dir(b.sambaConfig), unmanagedShadow, 0600)
	if err != nil {
		return err
	}
	defer os.Remove(unmanagedFile)
	unmanaged, err := b.sambaEffective(ctx, unmanagedFile)
	if err != nil {
		return err
	}
	for _, managed := range append(append([]Definition{}, defs...), previous...) {
		for _, other := range unmanaged {
			if managed.Name == shareKey(other.Name) {
				return errors.New("An unmanaged Samba section conflicts with a managed share name")
			}
		}
	}
	effective, err := b.sambaEffective(ctx, file)
	if err != nil {
		return err
	}
	for _, d := range defs {
		found := false
		for _, actual := range effective {
			if shareKey(actual.Name) == d.Name {
				found = sameSamba(d, actual)
				break
			}
		}
		if !found {
			return errors.New("Samba effective configuration does not match the managed share; check includes and overrides")
		}
	}
	// Avoid publishing against a main file changed during validation.
	latest, err := readFile(b.sambaConfig, false)
	if err != nil || !bytes.Equal(main.data, latest.data) {
		return errors.New("Samba main config changed during validation")
	}
	return nil
}
func (b *Backend) sambaConnections(ctx context.Context) (map[string]int, error) {
	if b.programs["smbstatus"] == "" {
		return nil, errors.New("smbstatus is unavailable")
	}
	text, err := b.runner.Capture(ctx, core.Command{Program: b.programs["smbstatus"], Args: []string{"--json", "--shares", "--configfile", b.sambaConfig}, Timeout: 3 * time.Second})
	if err != nil {
		return nil, err
	}
	var status struct {
		Tcons map[string]struct {
			Service string `json:"service"`
		} `json:"tcons"`
	}
	if !strings.HasPrefix(strings.TrimSpace(text), "{") || json.Unmarshal([]byte(text), &status) != nil {
		return nil, errors.New("unsupported Samba status JSON")
	}
	result := map[string]int{}
	for _, conn := range status.Tcons {
		result[shareKey(conn.Service)]++
	}
	return result, nil
}
func (b *Backend) reloadSamba(ctx context.Context) error {
	return b.runner.Run(ctx, core.Command{Program: b.programs["smbcontrol"], Args: []string{"--configfile", b.sambaConfig, "--timeout", "5", "smbd", "reload-config"}, Timeout: 7 * time.Second}, io.Discard)
}
