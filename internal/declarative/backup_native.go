package declarative

import (
	"context"
	"path/filepath"
	"strings"
)

type NativeBackupObject struct {
	Backend     string `json:"backend"`
	Name        string `json:"name"`
	Project     string `json:"project,omitempty"`
	Pool        string `json:"pool,omitempty"`
	ContentType string `json:"contentType,omitempty"`
}
type NativeBackupDependencies struct {
	State        string
	Object       *NativeBackupObject
	Inputs       []BackupInput
	Issues       []Issue
	Requirements []BackupRequirement
}
type BackupDependencyAdapter interface {
	BackupDependencies(context.Context, Config, string) (NativeBackupDependencies, error)
}

func (d *NativeBackupDependencies) issue(resource, code, message, remedy string) {
	if len(d.Issues) > 128 {
		return
	}
	if len(d.Issues) == 128 {
		d.Issues = append(d.Issues, Issue{"unknown", resource, "native_state", "backup_issue_limit", "Additional native dependency issues exceed the detail limit; coverage remains unresolved.", "Resolve reported issues and recheck native state before preparing a backup."})
		return
	}
	d.Issues = append(d.Issues, Issue{"unknown", resource, "native_state", code, message, remedy})
}
func (d *NativeBackupDependencies) require(resource, kind, description string) {
	d.Requirements = append(d.Requirements, BackupRequirement{resource, kind, description})
}
func nativeBackupPath(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && len(path) <= 4096 && !strings.ContainsAny(path, "\x00\r\n\t")
}

// Only explicitly classified safe policy messages may cross the native boundary.
type backupDiscoveryError struct{ message, remedy string }

func (e *backupDiscoveryError) Error() string { return e.message }
