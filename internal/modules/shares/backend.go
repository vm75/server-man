package shares

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"serverman/internal/core"
)

func (b *Backend) List(ctx context.Context, protocol string) (Inventory, error) {
	result := Inventory{Backends: []BackendInfo{}, Shares: []Share{}, Warnings: []string{}}
	if protocol != "" && protocol != "samba" && protocol != "nfs" {
		return result, core.Invalid("Unsupported share protocol")
	}
	for _, p := range []string{"samba", "nfs"} {
		if protocol != "" && protocol != p {
			continue
		}
		info := BackendInfo{Protocol: p, ManagedFile: b.file(p)}
		owned, err := readFile(info.ManagedFile, true)
		if err != nil {
			info.Reason = "Managed fragment is inaccessible or is a symlink"
		} else {
			info.Revision = owned.revision()
		}
		defs, loadErr := load(p, owned)
		if loadErr != nil {
			info.Reason = loadErr.Error()
		}
		if p == "samba" {
			info.Available = b.programs["testparm"] != ""
			info.CanManage = info.Available && b.programs["smbcontrol"] != "" && info.Reason == ""
			main, mainErr := readFile(b.sambaConfig, false)
			if mainErr != nil || !main.exists {
				info.SetupRequired = true
				result.Warnings = append(result.Warnings, "Samba main configuration is inaccessible or missing")
			} else if _, err := b.shadow(main.data, b.sambaManaged); err != nil {
				info.SetupRequired = true
				result.Warnings = append(result.Warnings, err.Error())
			}
			info.CanManage = info.CanManage && !info.SetupRequired
			if !info.Available {
				info.Reason = "testparm is unavailable; install Samba tools and restart server-man"
			} else if b.programs["smbcontrol"] == "" {
				info.Reason = "smbcontrol is unavailable; inventory can be read but changes cannot be applied"
			}
			effective := []Definition{}
			if info.Available && mainErr == nil && main.exists {
				var err error
				effective, err = b.sambaEffective(ctx, b.sambaConfig)
				if err != nil {
					result.Warnings = append(result.Warnings, err.Error())
					info.CanManage = false
				}
			}
			connections, connErr := b.sambaConnections(ctx)
			if connErr != nil {
				result.Warnings = append(result.Warnings, "Samba connection state is unavailable; check smbstatus access")
			}
			seen := map[string]Definition{}
			reported := map[string]bool{}
			for _, d := range defs {
				s := Share{Definition: d, Protocol: p, Managed: true, Runtime: "unknown"}
				seen[d.Name] = d
				reported[d.Name] = true
				for _, actual := range effective {
					if shareKey(actual.Name) == d.Name {
						s.Configured = sameSamba(actual, d)
						break
					}
				}
				if connErr == nil {
					n := connections[d.Name]
					s.Connections = &n
					s.Runtime = "not_observed"
					if n > 0 {
						s.Runtime = "connected"
					}
				}
				result.Shares = append(result.Shares, s)
			}
			for _, d := range effective {
				if owned, ok := seen[shareKey(d.Name)]; ok && sameSamba(owned, d) {
					continue
				}
				reported[shareKey(d.Name)] = true
				s := Share{Definition: d, Protocol: p, Configured: true, Runtime: "unknown"}
				if connErr == nil {
					n := connections[shareKey(d.Name)]
					s.Connections = &n
					s.Runtime = "not_observed"
					if n > 0 {
						s.Runtime = "connected"
					}
				}
				result.Shares = append(result.Shares, s)
			}
			for name, n := range connections {
				if !reported[name] && n > 0 {
					count := n
					result.Shares = append(result.Shares, Share{Definition: Definition{Name: name, ReadOnly: true}, Protocol: p, Configured: false, Runtime: "connected", Connections: &count})
				}
			}
			info.Notice = "Connection counts show current use, not complete daemon configuration. Existing sessions can retain old settings after reload."
		} else {
			info.Available = b.programs["exportfs"] != ""
			info.CanManage = info.Available && info.Reason == ""
			if !info.Available {
				info.Reason = "exportfs is unavailable; install the NFS server utilities and restart server-man"
			}
			if stat, err := os.Stat(b.nfsDir); err != nil || !stat.IsDir() {
				info.SetupRequired = true
				info.CanManage = false
			}
			configured, err := b.nfsConfigured()
			if err != nil {
				result.Warnings = append(result.Warnings, "Unmanaged NFS configuration could not be fully read; changes are blocked")
				info.CanManage = false
			}
			active, activeErr := b.nfsActive(ctx)
			if activeErr != nil {
				result.Warnings = append(result.Warnings, "NFS export table is unavailable; check exportfs permissions")
				info.CanManage = false
			}
			registered := exportMap(active)
			ownedPairs := pairs(defs)
			for _, d := range defs {
				s := Share{Definition: d, Protocol: p, Managed: true, Configured: true, Runtime: "unknown"}
				if activeErr == nil {
					s.Runtime = "exported"
					for key, want := range pairs([]Definition{d}) {
						actual, ok := registered[key]
						if !ok {
							s.Runtime = "not_exported"
							break
						}
						if !sameExport(actual, want) {
							s.Runtime = "mismatch"
						}
					}
				}
				result.Shares = append(result.Shares, s)
			}
			unmanagedConfigured := exportMap(configured)
			all := append(append([]export{}, configured...), active...)
			displayed := map[string]bool{}
			for _, e := range all {
				_, owned := ownedPairs[e.key()]
				_, unmanaged := unmanagedConfigured[e.key()]
				if (owned && !unmanaged) || displayed[e.key()] {
					continue
				}
				displayed[e.key()] = true
				s := Share{Definition: Definition{Name: e.path, Path: e.path, ReadOnly: flag(e.options, "ro", "rw", true), Clients: []string{e.client}, RootSquash: flag(e.options, "root_squash", "no_root_squash", true), Security: optionValue(e.options, "sec", "sys")}, Protocol: p, Runtime: "unknown"}
				for _, c := range configured {
					if c.key() == e.key() {
						s.Configured = true
						break
					}
				}
				if activeErr == nil {
					s.Runtime = "not_exported"
					if _, ok := registered[e.key()]; ok {
						s.Runtime = "exported"
					}
				}
				result.Shares = append(result.Shares, s)
			}
			info.Notice = "Runtime state reflects the registered export table, not a verified client mount. Filesystem permissions and NFS server setup still apply."
		}
		result.Backends = append(result.Backends, info)
	}
	slices.SortFunc(result.Shares, func(a, c Share) int {
		if a.Protocol != c.Protocol {
			return strings.Compare(a.Protocol, c.Protocol)
		}
		return strings.Compare(a.Name, c.Name)
	})
	return result, nil
}

func (b *Backend) Change(ctx context.Context, protocol, revision, name string, replacement *Definition, out io.Writer) (any, error) {
	if protocol != "samba" && protocol != "nfs" {
		return nil, core.Invalid("Unsupported share protocol")
	}
	if !validName(name) {
		return nil, core.Invalid("Invalid managed share name")
	}
	name = strings.ToLower(name)
	file := b.file(protocol)
	old, err := readFile(file, true)
	if err != nil {
		return nil, errors.New("Managed fragment is inaccessible or is a symlink")
	}
	if revision != old.revision() {
		return nil, &core.Error{Code: "conflict", Message: "Share configuration changed; refresh before saving", Status: 409}
	}
	defs, err := load(protocol, old)
	if err != nil {
		return nil, err
	}
	before, after := []Definition{}, []Definition{}
	next := []Definition{}
	for _, d := range defs {
		if d.Name == name {
			before = append(before, d)
		} else {
			next = append(next, d)
		}
	}
	if replacement != nil {
		d, err := validate(protocol, *replacement, true)
		if err != nil {
			return nil, err
		}
		if d.Name != name {
			return nil, core.Invalid("Share identity does not match")
		}
		for _, other := range next {
			if protocol == "nfs" && other.Path == d.Path {
				return nil, core.Invalid("A managed NFS export already owns this path")
			}
		}
		after = append(after, d)
		next = append(next, d)
	} else if len(before) == 0 {
		return nil, &core.Error{Code: "not_found", Message: "Managed share not found", Status: 404}
	}
	result, err := b.publishDefinitions(ctx, protocol, old, defs, next, before, after, out)
	if err != nil {
		return nil, err
	}
	return map[string]any{"protocol": protocol, "name": name, "removed": replacement == nil, "revision": result}, nil
}

// publishDefinitions is shared by direct host share operations and authoritative
// replacement. defs are observed outputs for integrity/rollback, never desired input.
func (b *Backend) publishDefinitions(ctx context.Context, protocol string, old snapshot, defs, next, before, after []Definition, out io.Writer) (any, error) {
	return b.definitionsTransaction(ctx, protocol, old, defs, next, before, after, out, true)
}
func (b *Backend) definitionsTransaction(ctx context.Context, protocol string, old snapshot, defs, next, before, after []Definition, out io.Writer, activate bool) (any, error) {
	file := b.file(protocol)
	desiredNames := map[string]bool{}
	for _, d := range after {
		desiredNames[d.Name] = true
	}
	data, err := render(protocol, next)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var active []export
	if protocol == "samba" {
		if b.programs["testparm"] == "" || b.programs["smbcontrol"] == "" {
			return nil, core.Unavailable("Samba testparm and smbcontrol are required")
		}
		effective, err := b.sambaEffective(ctx, b.sambaConfig)
		if err != nil {
			return nil, err
		}
		owned := map[string]bool{}
		for _, d := range defs {
			owned[d.Name] = true
		}
		for _, d := range effective {
			if desiredNames[shareKey(d.Name)] && !owned[shareKey(d.Name)] {
				return nil, core.Invalid("An unmanaged Samba share already uses this name")
			}
		}
	} else {
		if b.programs["exportfs"] == "" {
			return nil, core.Unavailable("exportfs is required")
		}
		configured, err := b.nfsConfigured()
		if err != nil {
			return nil, errors.New("Cannot safely inspect unmanaged NFS exports")
		}
		active, err = b.nfsActive(ctx)
		if err != nil {
			return nil, errors.New("Cannot snapshot the current NFS export table")
		}
		oldPairs := pairs(before)
		touched := pairs(before)
		for key, e := range pairs(after) {
			touched[key] = e
		}
		for _, e := range active {
			if _, changes := touched[e.key()]; changes && !safeOptions.MatchString(strings.Join(e.options, ",")) {
				return nil, errors.New("Existing runtime options cannot be safely restored; resolve them with native NFS tools first")
			}
		}
		for _, d := range append(append([]Definition{}, before...), after...) {
			for _, e := range configured {
				if e.path == d.Path {
					return nil, core.Invalid("An unmanaged NFS export already uses this path")
				}
			}
		}
		for _, d := range after {
			for _, e := range active {
				if e.path == d.Path {
					if _, owned := oldPairs[e.key()]; !owned {
						return nil, core.Invalid("An unmanaged runtime NFS export already uses this path")
					}
				}
			}
		}
	}
	_, _ = io.WriteString(out, "Validating managed share configuration…\n")
	mode := os.FileMode(0644)
	if old.exists {
		mode = old.mode
	}
	candidate, err := temporary(filepath.Dir(file), data, mode)
	if err != nil {
		return nil, fmt.Errorf("Create managed fragment candidate: %w", err)
	}
	defer os.Remove(candidate)
	if protocol == "samba" {
		if err := b.validateSamba(ctx, next, defs, candidate); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !sameFile(file, old) {
		return nil, errors.New("Managed fragment changed during validation; refresh and retry")
	}
	if !activate {
		return old.revision(), nil
	}
	if err := publish(file, candidate); err != nil {
		// A failed directory sync can follow a successful rename.
		if current, readErr := readFile(file, true); readErr == nil && current.exists && string(current.data) == string(data) {
			if rollbackErr := restore(file, old, data); rollbackErr != nil {
				return nil, fmt.Errorf("Publish failed and file rollback failed: %v", rollbackErr)
			}
		}
		return nil, fmt.Errorf("Publish managed fragment: %w", err)
	}
	if protocol == "samba" {
		err = b.reloadSamba(ctx)
	} else {
		err = b.applyNFS(ctx, before, after, active, out)
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		_, _ = io.WriteString(out, "Apply failed; restoring previous configuration and runtime entries…\n")
		fileErr := restore(file, old, data)
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var runtimeErr error
		if fileErr == nil {
			if protocol == "samba" {
				runtimeErr = b.reloadSamba(rollbackCtx)
			} else {
				runtimeErr = b.restoreNFS(rollbackCtx, before, after, active, out)
			}
		}
		if fileErr != nil || runtimeErr != nil {
			return nil, fmt.Errorf("Share apply failed; rollback incomplete (file: %v, runtime: %v). Inspect host state before retrying", fileErr, runtimeErr)
		}
		return nil, errors.New("Share apply failed; previous configuration and runtime entries were restored")
	}
	_, _ = io.WriteString(out, "Managed share configuration applied. Shared directory data was not changed.\n")
	return snapshot{data: data, exists: true}.revision(), nil
}
