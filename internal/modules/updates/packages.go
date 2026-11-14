package updates

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"serverman/internal/core"
	"strings"
	"time"
)

var installPackageName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9+_.-]{0,127}$`)

// InstallPackages accepts repository package names, never paths, URLs, options,
// repository selectors or package-manager configuration overrides.
func (b *Backend) InstallPackages(ctx context.Context, names string, out io.Writer) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	packages := strings.Fields(names)
	if len(packages) == 0 || len(packages) > 32 {
		return nil, core.Invalid("provide between 1 and 32 package names")
	}
	seen := map[string]bool{}
	for _, name := range packages {
		if !installPackageName.MatchString(name) || seen[name] {
			return nil, core.Invalid("invalid or duplicate repository package name")
		}
		seen[name] = true
	}
	if !b.manager.Available {
		return nil, core.Unavailable(b.manager.Reason)
	}
	if b.manager.ID != "pacman" {
		if _, err := b.Refresh(ctx, out); err != nil {
			return nil, err
		}
	}
	c := core.Command{Program: b.program, Timeout: 20 * time.Minute, CancelGrace: 30 * time.Second, Env: []string{"DEBIAN_FRONTEND=noninteractive", "NEEDRESTART_MODE=l", "NO_COLOR=1"}}
	switch b.manager.ID {
	case "apt":
		c.Args = []string{"--assume-yes", "--no-remove", "--no-install-recommends", "-o", "Dpkg::Options::=--force-confdef", "-o", "Dpkg::Options::=--force-confold", "install", "--"}
	case "dnf":
		c.Args = []string{"--assumeyes", "install", "--"}
	case "pacman":
		c.Args = []string{"--sync", "--refresh", "--sysupgrade", "--needed", "--noconfirm", "--"}
	case "zypper":
		c.Args = []string{"--non-interactive", "--no-refresh", "install", "--"}
	default:
		return nil, core.Unavailable("unsupported package manager")
	}
	c.Args = append(c.Args, packages...)
	if err := b.runner.Run(ctx, c, out); err != nil {
		return nil, fmt.Errorf("Package installation failed: %w", err)
	}
	return map[string]any{"manager": b.manager.ID, "action": "packages installed", "packages": packages}, nil
}
