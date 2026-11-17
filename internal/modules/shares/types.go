// Package shares manages its own Samba/NFS fragments and reads host share metadata.
package shares

import (
	"os/exec"

	"server-man/internal/config"
	"server-man/internal/core"
)

const maxFile = 1 << 20
const header = "# Managed by server-man. Do not edit.\n# server-man-format: 1\n"

// Definition is the complete supported format. Credentials and arbitrary options
// deliberately have no place in it. Native configuration remains administrator-owned.
type Definition struct {
	Name       string   `json:"name"`
	Path       string   `json:"path"`
	ReadOnly   bool     `json:"readOnly"`
	Guest      bool     `json:"guest,omitempty"`
	Browseable bool     `json:"browseable,omitempty"`
	Users      []string `json:"users,omitempty"`
	Clients    []string `json:"clients,omitempty"`
	RootSquash bool     `json:"rootSquash,omitempty"`
	Security   string   `json:"security,omitempty"`
}
type Share struct {
	Definition
	Protocol    string `json:"protocol"`
	Managed     bool   `json:"managed"`
	Configured  bool   `json:"configured"`
	Runtime     string `json:"runtime"`
	Connections *int   `json:"connections,omitempty"`
}
type BackendInfo struct {
	Protocol      string `json:"protocol"`
	Available     bool   `json:"available"`
	CanManage     bool   `json:"canManage"`
	Reason        string `json:"reason,omitempty"`
	ManagedFile   string `json:"managedFile"`
	Revision      string `json:"revision"`
	SetupRequired bool   `json:"setupRequired"`
	Notice        string `json:"notice,omitempty"`
}
type Inventory struct {
	Backends []BackendInfo `json:"backends"`
	Shares   []Share       `json:"shares"`
	Warnings []string      `json:"warnings"`
}
type Backend struct {
	sambaConfig, sambaManaged, nfsExports, nfsDir, nfsManaged string
	programs                                                  map[string]string
	runner                                                    core.Executor
}

func New(cfg config.Config, runner core.Executor) *Backend {
	b := &Backend{sambaConfig: cfg.SambaConfig, sambaManaged: cfg.SambaManagedFile, nfsExports: "/etc/exports", nfsDir: "/etc/exports.d", nfsManaged: "/etc/exports.d/server-man.exports", programs: map[string]string{}, runner: runner}
	if b.sambaConfig == "" {
		b.sambaConfig = "/etc/samba/smb.conf"
	}
	if b.sambaManaged == "" {
		b.sambaManaged = "/etc/samba/server-man.conf"
	}
	for _, name := range []string{"testparm", "smbcontrol", "smbstatus", "exportfs", "wsdd"} {
		if path, err := exec.LookPath(name); err == nil {
			b.programs[name] = path
		}
	}
	return b
}
func (b *Backend) file(protocol string) string {
	if protocol == "samba" {
		return b.sambaManaged
	}
	return b.nfsManaged
}
