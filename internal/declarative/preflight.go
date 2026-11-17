package declarative

import (
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"time"
)

// Issue never carries file contents or resolved environment values.
type Issue struct {
	Severity string `json:"severity"`
	Resource string `json:"resource"`
	Field    string `json:"field"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Remedy   string `json:"remedy"`
}
type Health struct {
	CheckedAt        time.Time `json:"checkedAt"`
	RuntimeCheckedAt time.Time `json:"runtimeCheckedAt,omitempty"`
	Issues           []Issue   `json:"issues"`
}

// A successful access(2) probe (especially as root) does not prove that a
// writable storage reference lives on a writable filesystem. Unknown mount
// inventory also must not be mistaken for a healthy relocated workload.
func writableMountIssue(resource, path string, mounts []MountInfo, inventoryErr error) *Issue {
	unknown := func() *Issue {
		return &Issue{
			Severity: "unknown", Resource: resource, Field: "path", Code: "mount_state_unavailable",
			Message: "The filesystem write state could not be verified.",
			Remedy:  "Restore access to /proc/self/mountinfo and recheck before starting workloads that write to this storage.",
		}
	}
	if inventoryErr != nil {
		return unknown()
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil // Missing or inaccessible paths have their own diagnostics.
	}
	mount, ok := mountedFilesystem(resolved, mounts)
	if !ok {
		return unknown()
	}
	if mount.ReadOnly {
		return &Issue{
			Severity: "error", Resource: resource, Field: "path", Code: "read_only_mount",
			Message: "Required writable storage is on a read-only filesystem.",
			Remedy:  "Restore a writable filesystem mount or rebind this storage to a writable location, then check configuration.",
		}
	}
	return nil
}

// CheckFiles is the static filesystem part of preflight. Runtime/backend checks
// are separate; an empty result here is not a claim that a service can start.
// It deliberately never creates directories or substitutes an empty disk.
func (s Store) CheckFiles(c Config) Health {
	h := Health{CheckedAt: time.Now().UTC(), Issues: []Issue{}}
	add := func(resource, field, code, message, remedy string) {
		h.Issues = append(h.Issues, Issue{"error", resource, field, code, message, remedy})
	}
	var mounts []MountInfo
	mountFile, mountErr := os.Open("/proc/self/mountinfo")
	if mountErr == nil {
		mounts, mountErr = ParseMountInfo(mountFile)
		_ = mountFile.Close()
	}
	check := func(resource, field, path, kind string, private bool) {
		resolved := s.Resolve(path)
		if !filepath.IsAbs(resolved) {
			add(resource, field, "invalid_root", "The configuration location must be absolute.", "Select an absolute manifest path.")
			return
		}
		if !filepath.IsAbs(path) {
			root, err := filepath.EvalSymlinks(filepath.Dir(s.Path))
			if err != nil {
				add(resource, field, "unavailable_root", "The configuration directory is inaccessible.", "Restore the configuration directory and its access permissions.")
				return
			}
			real, err := filepath.EvalSymlinks(resolved)
			if err == nil {
				rel, err := filepath.Rel(root, real)
				if err != nil || rel == ".." || len(rel) > 3 && rel[:3] == "../" {
					add(resource, field, "path_escape", "A portable path resolves outside the configuration directory.", "Replace the escaping link or declare external storage explicitly.")
					return
				}
			}
		}
		info, err := os.Stat(resolved)
		if err != nil {
			code := "inaccessible_path"
			message := "A required path cannot be accessed."
			if os.IsNotExist(err) {
				code = "missing_path"
				message = "A required file or folder is missing."
			}
			add(resource, field, code, message, "Restore or rebind the required data; do not replace it with empty data.")
			return
		}
		if (kind == "file" && !info.Mode().IsRegular()) || (kind == "directory" && !info.IsDir()) {
			add(resource, field, "wrong_type", "A required path has the wrong file type.", "Select the correct file or directory.")
			return
		}
		mode := uint32(4)
		if kind == "directory" {
			mode |= 1
		}
		if err := syscall.Access(resolved, mode); err != nil {
			add(resource, field, "path_permissions", "A required path cannot be read or traversed by the service account.", "Restore access permissions or select the correct service account.")
		}
		if private && info.Mode().Perm()&0077 != 0 {
			add(resource, field, "secret_permissions", "A referenced private file allows group or other access.", "Restrict file permissions to the service account.")
		}
	}
	writable := map[string]bool{}
	for _, service := range c.Services {
		for _, container := range service.Containers {
			for _, mount := range container.Mounts {
				if !mount.ReadOnly {
					writable[mount.Storage] = true
				}
			}
		}
		if service.VM != nil {
			for _, disk := range service.VM.Disks {
				writable[disk] = true
			}
		}
	}
	for _, share := range c.Shares {
		if !share.ReadOnly {
			writable[share.Storage] = true
		}
	}
	for id, st := range c.Storage {
		kind := st.Kind
		if kind == "" {
			kind = "directory"
		}
		check("storage/"+id, "path", st.Path, kind, false)
		if st.Source != nil {
			firstIssue := len(h.Issues)
			check("storage/"+id, "source.path", st.Source.Path, "file", false)
			if info, e := os.Stat(s.Resolve(st.Path)); e == nil && info.Mode().IsRegular() {
				for index := firstIssue; index < len(h.Issues); index++ {
					h.Issues[index].Severity = "warning"
					h.Issues[index].Remedy = "Restore the initial image asset before future provisioning or backup; the existing disk is never reinitialized."
				}
			}
		}
		if writable[id] {
			if info, err := os.Stat(s.Resolve(st.Path)); err == nil && (info.IsDir() || info.Mode().IsRegular()) {
				if err = syscall.Access(s.Resolve(st.Path), 2); err != nil {
					add("storage/"+id, "path", "storage_not_writable", "The service account cannot write required persistent data.", "Restore ownership, ACLs and mount write access before starting the workload.")
				}
				if issue := writableMountIssue("storage/"+id, s.Resolve(st.Path), mounts, mountErr); issue != nil {
					h.Issues = append(h.Issues, *issue)
				}
			}
		}
		if st.Mount != "" {
			if mountErr != nil {
				h.Issues = append(h.Issues, Issue{"unknown", "storage/" + id, "mount", "mount_inventory_unavailable", "Mounted filesystem identity could not be checked.", "Restore access to the host mount inventory and recheck."})
			} else if !filepath.IsAbs(st.Mount) {
				add("storage/"+id, "mount", "mount_check_pending", "The expected mounted filesystem requires an absolute device source.", "Set mount to the expected mount source, such as /dev/mapper/data.")
			} else {
				path, err := filepath.EvalSymlinks(s.Resolve(st.Path))
				if err != nil || mountedSource(path, mounts) != st.Mount {
					add("storage/"+id, "mount", "missing_mount", "The required filesystem is not mounted at the selected data path.", "Mount the expected source before starting services; an empty mountpoint is not restored data.")
				}
			}
		}
	}
	for id, path := range c.Secrets {
		check("secret/"+id, "path", path, "file", true)
	}
	// Propagate storage/file blockers so the UI can explain affected applications.
	initial := slices.Clone(h.Issues)
	for id, svc := range c.Services {
		resources := map[string]bool{}
		for _, reference := range serviceDataReferences(svc) {
			resources[reference] = true
		}
		for _, issue := range initial {
			if resources[issue.Resource] {
				if issue.Severity == "warning" {
					h.Issues = append(h.Issues, Issue{"warning", "service/" + id, issue.Resource, "initial_asset_unavailable", "An initial disk image asset needs attention; the existing disk remains authoritative data.", issue.Remedy})
					continue
				}
				add("service/"+id, issue.Resource, "blocked_reference", "A required storage or private-file reference is blocked.", issue.Remedy)
			}
		}
	}
	for id, share := range c.Shares {
		for _, issue := range initial {
			if issue.Resource == "storage/"+share.Storage {
				add("share/"+id, "storage", "blocked_reference", "The shared directory is unavailable or requires verification.", issue.Remedy)
			}
		}
	}
	h.Issues = append(h.Issues, publishedPortIssues(c)...)
	slices.SortFunc(h.Issues, func(a, b Issue) int {
		if a.Resource != b.Resource {
			if a.Resource < b.Resource {
				return -1
			}
			return 1
		}
		if a.Field < b.Field {
			return -1
		}
		if a.Field > b.Field {
			return 1
		}
		return 0
	})
	return h
}
