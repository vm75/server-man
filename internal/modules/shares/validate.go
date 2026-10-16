package shares

import (
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"serverman/internal/core"
)

var namePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$`)
var userPattern = regexp.MustCompile(`^@?[a-zA-Z_][a-zA-Z0-9_.-]{0,63}\$?$`)

func validName(name string) bool {
	return namePattern.MatchString(name) && !slices.Contains([]string{"global", "homes", "printers", "ipc$"}, strings.ToLower(name))
}
func validPath(path string) bool {
	if !filepath.IsAbs(path) || len(path) > 4096 || filepath.Clean(path) != path || strings.TrimSpace(path) != path || strings.ContainsAny(path, "\\%\"'[]#;(),") {
		return false
	}
	return !strings.ContainsFunc(path, func(r rune) bool { return unicode.IsControl(r) })
}
func client(value string) (string, error) {
	if value == "*" {
		return value, nil
	}
	if addr, err := netip.ParseAddr(value); err == nil && addr.Zone() == "" {
		return netip.PrefixFrom(addr, addr.BitLen()).String(), nil
	}
	if prefix, err := netip.ParsePrefix(value); err == nil {
		return prefix.Masked().String(), nil
	}
	return "", core.Invalid("NFS clients must be IP addresses, CIDR networks or an explicit *")
}
func tokens(value string) []string {
	return strings.FieldsFunc(value, func(r rune) bool { return r == ',' || unicode.IsSpace(r) })
}

func validate(protocol string, d Definition, checkPath bool) (Definition, error) {
	if protocol != "samba" && protocol != "nfs" {
		return d, core.Invalid("unsupported share protocol")
	}
	if !validName(d.Name) {
		return d, core.Invalid("Share name must be a portable identifier; reserved Samba sections are unsupported")
	}
	d.Name = strings.ToLower(d.Name)
	if !validPath(d.Path) {
		return d, core.Invalid("Share path must be a clean absolute literal directory path without configuration metacharacters")
	}
	if checkPath {
		real, err := filepath.EvalSymlinks(d.Path)
		if err != nil || !validPath(real) {
			return d, core.Invalid("Share directory is inaccessible or resolves to an unsupported path")
		}
		info, err := os.Stat(real)
		if err != nil || !info.IsDir() {
			return d, core.Invalid("Share path must be an existing directory")
		}
		d.Path = real
	}
	if protocol == "samba" {
		if len(d.Clients) > 0 || d.Security != "" || d.RootSquash {
			return d, core.Invalid("NFS settings cannot be used in a Samba share")
		}
		if len(d.Users) > 32 || (!d.Guest && len(d.Users) == 0) {
			return d, core.Invalid("Samba requires explicit users/groups unless guest access is enabled; limit 32")
		}
		if d.Guest && len(d.Users) > 0 {
			return d, core.Invalid("Guest shares must not also specify a valid-users restriction")
		}
		for _, user := range d.Users {
			if !userPattern.MatchString(user) {
				return d, core.Invalid("Samba users must be simple account names or @group names")
			}
		}
		slices.Sort(d.Users)
		d.Users = slices.Compact(d.Users)
	} else {
		if d.Guest || d.Browseable || len(d.Users) > 0 {
			return d, core.Invalid("Samba settings cannot be used in an NFS export")
		}
		if !slices.Contains([]string{"sys", "krb5", "krb5i", "krb5p"}, d.Security) {
			return d, core.Invalid("Unsupported NFS security flavor")
		}
		if len(d.Clients) == 0 || len(d.Clients) > 16 {
			return d, core.Invalid("NFS requires 1 to 16 explicit clients/networks")
		}
		for i, value := range d.Clients {
			normalized, err := client(value)
			if err != nil {
				return d, err
			}
			d.Clients[i] = normalized
		}
		slices.Sort(d.Clients)
		d.Clients = slices.Compact(d.Clients)
	}
	return d, nil
}

func options(d Definition) string {
	access, squash := "ro", "root_squash"
	if !d.ReadOnly {
		access = "rw"
	}
	if !d.RootSquash {
		squash = "no_root_squash"
	}
	return fmt.Sprintf("%s,sync,%s,no_all_squash,no_subtree_check,secure,sec=%s", access, squash, d.Security)
}
func target(selector, path string) string {
	if strings.Contains(selector, ":") {
		parts := strings.SplitN(selector, "/", 2)
		selector = "[" + parts[0] + "]"
		if len(parts) == 2 {
			selector += "/" + parts[1]
		}
	}
	return selector + ":" + path
}
