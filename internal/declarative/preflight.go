package declarative

import (
	"os"
	"path/filepath"
	"slices"
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
	CheckedAt time.Time `json:"checkedAt"`
	Issues    []Issue   `json:"issues"`
}

// CheckFiles is the static filesystem part of preflight. Runtime/backend checks
// are separate; an empty result here is not a claim that a service can start.
// It deliberately never creates directories or substitutes an empty disk.
func (s Store) CheckFiles(c Config) Health {
	h := Health{CheckedAt: time.Now().UTC(), Issues: []Issue{}}
	add := func(resource, field, code, message, remedy string) {
		h.Issues = append(h.Issues, Issue{"error", resource, field, code, message, remedy})
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
		if private && info.Mode().Perm()&0077 != 0 {
			add(resource, field, "secret_permissions", "A referenced private file allows group or other access.", "Restrict file permissions to the service account.")
		}
	}
	for id, st := range c.Storage {
		kind := st.Kind
		if kind == "" {
			kind = "directory"
		}
		check("storage/"+id, "path", st.Path, kind, false)
		if st.Mount != "" {
			add("storage/"+id, "mount", "mount_check_pending", "The expected mounted filesystem has not been verified.", "Verify mount identity before starting dependent services.")
		}
	}
	for id, path := range c.Secrets {
		check("secret/"+id, "path", path, "file", true)
	}
	// Propagate storage/file blockers so the UI can explain affected applications.
	initial := slices.Clone(h.Issues)
	for id, svc := range c.Services {
		resources := map[string]bool{}
		for _, v := range svc.Containers {
			for _, m := range v.Mounts {
				resources["storage/"+m.Storage] = true
			}
			for _, ref := range v.EnvFiles {
				resources["secret/"+ref] = true
			}
		}
		if svc.VM != nil {
			for _, disk := range svc.VM.Disks {
				resources["storage/"+disk] = true
			}
		}
		for _, issue := range initial {
			if resources[issue.Resource] {
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
