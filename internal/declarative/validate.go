package declarative

import (
	"errors"
	"net"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var userName = regexp.MustCompile(`^@?[a-zA-Z_][a-zA-Z0-9_.-]{0,63}$`)

func literal(s string) bool { return !strings.ContainsFunc(s, unicode.IsControl) }
func validPort(s string) bool {
	parts := strings.Split(s, "/")
	if len(parts) > 2 {
		return false
	}
	if len(parts) == 2 && parts[1] != "tcp" && parts[1] != "udp" {
		return false
	}
	ports := strings.Split(parts[0], ":")
	if len(ports) < 1 || len(ports) > 2 {
		return false
	}
	for _, p := range ports {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return false
		}
	}
	return true
}
func (c Config) validateFields() error {
	uuids, macs := map[string]bool{}, map[string]bool{}
	containerNames := map[string]bool{}
	if len(c.Bindings) > 128 || len(c.Secrets) > 128 {
		return errors.New("configuration reference limit exceeded")
	}
	for _, s := range c.Storage {
		if s.Format != "" && (s.Kind != "file" || (s.Format != "qcow2" && s.Format != "raw")) {
			return errors.New("disk format requires file storage and qcow2 or raw")
		}
		if !literal(s.Path) || !literal(s.Mount) {
			return errors.New("invalid storage path characters")
		}
	}
	for _, s := range c.Services {
		if s.Kind == "container" {
			if err := validateContainerOptions(s.Engine, s.Backend, s.Containers); err != nil { return err }
		}
		if !literal(s.Name) || len(s.Name) > 128 || len(s.Description) > 4096 {
			return errors.New("invalid service display fields")
		}
		deps := map[string]bool{}
		for _, d := range s.DependsOn {
			if deps[d] {
				return errors.New("duplicate dependency")
			}
			deps[d] = true
		}
		for _, v := range s.Containers {
			if v.ContainerName != "" {
				if containerNames[v.ContainerName] { return errors.New("duplicate native container name") }
				containerNames[v.ContainerName] = true
			}
			if strings.HasPrefix(v.Image, "-") || strings.ContainsAny(v.Image, " \t\n\r\x00") || len(v.Image) > 512 {
				return errors.New("invalid container image")
			}
			if v.CPUs > 1024 || v.MemoryMiB > 1<<30 {
				return errors.New("container resource limit exceeded")
			}
			if len(v.Command) > 128 || len(v.Environment) > 128 || len(v.Ports) > 64 || len(v.Mounts) > 64 || len(v.EnvFiles) > 32 {
				return errors.New("container field limit exceeded")
			}
			for _, a := range v.Command {
				if strings.ContainsRune(a, 0) || len(a) > 8192 {
					return errors.New("invalid command argument")
				}
			}
			for k, v := range v.Environment {
				if !envName.MatchString(k) || strings.ContainsRune(v, 0) || len(v) > 8192 {
					return errors.New("invalid environment entry")
				}
			}
			for _, p := range v.Ports {
				if !validPort(p) {
					return errors.New("ports require port or host-port:container-port with optional tcp/udp")
				}
			}
			targets := map[string]bool{}
			for _, m := range v.Mounts {
				if !literal(m.Target) || targets[m.Target] {
					return errors.New("invalid or duplicate mount target")
				}
				targets[m.Target] = true
			}
		}
		if v := s.VM; v != nil {
			if v.RootSizeGiB < 0 || v.RootSizeGiB > 1<<20 {
				return errors.New("invalid VM root disk size")
			}
			if v.CPUs > 1024 || v.MemoryMiB > 1<<30 {
				return errors.New("VM resource limit exceeded")
			}
			if v.UUID != "" {
				if !uuidPattern.MatchString(v.UUID) || uuids[v.UUID] {
					return errors.New("invalid or duplicate VM UUID")
				}
				uuids[v.UUID] = true
			}
			if v.MAC != "" {
				m, err := net.ParseMAC(v.MAC)
				if err != nil || len(m) != 6 || m[0]&1 != 0 || m.String() != v.MAC || macs[v.MAC] {
					return errors.New("invalid or duplicate VM MAC")
				}
				macs[v.MAC] = true
			}
			b := c.Bindings[v.Network]
			if b.Kind != "network" && b.Kind != "bridge" {
				return errors.New("VM network binding has incorrect kind")
			}
			for ref, kind := range map[string]string{v.Pool: "pool", v.Project: "project"} {
				if ref != "" && c.Bindings[ref].Kind != kind {
					return errors.New("VM binding has incorrect kind")
				}
			}
			if v.Image == "" && len(v.Disks) == 0 {
				return errors.New("VM requires image or existing disk")
			}
		}
	}
	for _, s := range c.Shares {
		if s.Protocol == "samba" {
			if s.RootSquash != nil || s.Security != "" || s.Guest && len(s.Users) > 0 {
				return errors.New("invalid Samba access fields")
			}
			if len(s.Clients) > 0 || len(s.Users) > 32 {
				return errors.New("invalid Samba access fields")
			}
			for _, u := range s.Users {
				if !userName.MatchString(u) {
					return errors.New("invalid Samba access identity")
				}
			}
		} else {
			if s.Guest || s.Browseable {
				return errors.New("Samba options cannot be used for NFS")
			}
			if s.Security != "" && s.Security != "sys" && s.Security != "krb5" && s.Security != "krb5i" && s.Security != "krb5p" {
				return errors.New("invalid NFS security flavor")
			}
			if len(s.Users) > 0 || len(s.Clients) > 16 {
				return errors.New("invalid NFS access fields")
			}
			for _, client := range s.Clients {
				if client == "*" {
					continue
				}
				if _, err := netip.ParsePrefix(client); err != nil {
					if _, err = netip.ParseAddr(client); err != nil {
						return errors.New("NFS clients must be addresses or CIDRs")
					}
				}
			}
		}
	}
	return nil
}
