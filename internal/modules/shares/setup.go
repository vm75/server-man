package shares

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"serverman/internal/config"
	"serverman/internal/core"
	"serverman/internal/modules/services"
	"serverman/internal/modules/updates"
	"strings"
)

// RegisterSetup adds explicit native server installation/configuration. It is
// separate from share save/remove, which never change unmanaged configuration.
func RegisterSetup(r *core.Registry, cfg config.Config, runner core.Executor) error {
	return r.AddModule(core.Module{ID: "share_setup", Title: "Share server setup", Available: true}, core.Tool{
		Name: "shares_setup", Title: "Set up share server", Destructive: true,
		Description: "Install native Samba/NFS server packages, prepare managed configuration, enable and start its system service. Preserves existing configuration and share data. Does not configure firewall rules or Samba passwords. On Arch, package installation also upgrades the system.",
		Fields:      map[string]core.Field{"protocol": core.EnumField("Server protocol", true, "samba", "nfs")},
		Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
			protocol := in.String("protocol")
			unit, err := updates.New(cfg, runner).InstallShareServer(ctx, protocol, out)
			if err != nil {
				return nil, err
			}
			b := New(cfg, runner)
			if err = b.prepare(ctx, protocol); err != nil {
				return nil, err
			}
			service := services.New("system", runner)
			if _, err = service.Action(ctx, unit, "enable", out); err != nil {
				return nil, err
			}
			if _, err = service.Action(ctx, unit, "start", out); err != nil {
				return nil, err
			}
			return map[string]string{"protocol": protocol, "unit": unit, "action": "server configured and started"}, nil
		},
	})
}

// prepare only adds the required Samba include, or creates the native NFS
// fragment directory. All other native configuration and shared data survive.
func (b *Backend) prepare(ctx context.Context, protocol string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if protocol == "nfs" {
		return os.MkdirAll(b.nfsDir, 0755)
	}
	if protocol != "samba" {
		return core.Invalid("unsupported share protocol")
	}
	managed, err := readFile(b.sambaManaged, true)
	if err != nil {
		return err
	}
	if _, err = load("samba", managed); err != nil {
		return errors.New("Samba managed filename already contains unowned or invalid configuration")
	}
	original, err := readFile(b.sambaConfig, true)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(b.sambaConfig), 0755); err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(b.sambaManaged), 0755); err != nil {
		return err
	}
	// An existing include must already match the supported placement. Do not
	// move or rewrite an administrator's existing include directives.
	for _, line := range strings.Split(string(original.data), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && strings.EqualFold(strings.TrimSpace(key), "include") && strings.TrimSpace(value) == b.sambaManaged {
			_, err = b.shadow(original.data, b.sambaManaged)
			return err
		}
	}
	candidate := append(bytes.Clone(original.data), []byte("\ninclude = "+b.sambaManaged+"\n")...)
	if !original.exists {
		candidate = []byte("[global]\n\tserver role = standalone server\ninclude = " + b.sambaManaged + "\n")
	}
	file, err := temporary(filepath.Dir(b.sambaConfig), candidate, 0644)
	if err != nil {
		return err
	}
	defer os.Remove(file)
	if _, err = b.sambaEffective(ctx, file); err != nil {
		return err
	}
	current, err := readFile(b.sambaConfig, true)
	if err != nil {
		return err
	}
	if current.revision() != original.revision() {
		return errors.New("Samba configuration changed during setup; retry")
	}
	if original.exists {
		if err = os.Chmod(file, original.mode); err != nil {
			return err
		}
	}
	return publish(b.sambaConfig, file)
}
