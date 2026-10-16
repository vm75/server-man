package machines

import (
	"context"
	"io"

	"serverman/internal/core"
)

func (b *Backend) Register(r *core.Registry) error {
	nameFields := func() map[string]core.Field {
		return map[string]core.Field{"name": core.StringField("Machine name (portable identifier); libvirt also accepts a UUID", true, 128)}
	}
	for _, c := range b.capabilities() {
		if err := r.AddModule(core.Module{ID: c.ID, Title: c.Title, Available: c.Available, Reason: c.Reason}); err != nil {
			return err
		}
	}
	details := nameFields()
	details["backend"] = core.EnumField("Machine backend", true, "lxd", "lxc", "kvm")
	tools := []core.Tool{
		{Name: "machines_list", Title: "Machine inventory", Description: "List LXD, classic LXC and KVM/libvirt machines and independent backend capabilities. Returns selected metadata only, never full guest configuration.", ReadOnly: true, Fields: map[string]core.Field{"backend": core.EnumField("Optional backend filter", false, "lxd", "lxc", "kvm")}, Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
			return b.List(ctx, in.String("backend"))
		}},
		{Name: "machines_details", Title: "Machine details", Description: "Read selected machine state, addresses and available resource/autostart metadata.", ReadOnly: true, Fields: details, Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
			return b.Details(ctx, in.String("backend"), in.String("name"))
		}},
	}
	for backend, actions := range map[string][]string{"lxd": {"start", "stop", "restart", "delete"}, "lxc": {"start", "stop"}, "kvm": {"start", "shutdown", "reboot", "undefine"}} {
		for _, action := range actions {
			description := "Run " + backend + " " + action + " as a tracked job in the configured backend context."
			if action == "delete" {
				description += " Requires a stopped instance; permanently removes the instance, its snapshots and root storage. Attached custom volumes are governed by LXD."
			}
			if action == "undefine" {
				description += " Requires an inactive domain. Retains disks and NVRAM; existing snapshot/managed-save metadata may prevent removal."
			}
			if action == "shutdown" || action == "reboot" {
				description += " Requests a guest operation; successful command submission does not guarantee the guest completed it."
			}
			tools = append(tools, core.Tool{Name: backend + "_" + action, Title: backend + " " + action, Description: description, Destructive: action != "start", Fields: nameFields(), Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
				return b.Action(ctx, backend, in.String("name"), action, out)
			}})
		}
	}
	tools = append(tools,
		core.Tool{Name: "lxd_launch", Title: "Launch LXD instance", Description: "Create and start an LXD container or VM from a configured image alias/fingerprint; applies the backend's default profile and storage.", Fields: map[string]core.Field{"name": core.StringField("New instance name", true, 63), "image": core.StringField("Image alias/fingerprint, e.g. ubuntu:24.04", true, 256), "kind": core.EnumField("Instance type", true, "container", "virtual-machine")}, Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
			return b.Launch(ctx, in.String("name"), in.String("image"), in.String("kind"), out)
		}},
		core.Tool{Name: "kvm_autostart", Title: "Set domain autostart", Description: "Enable or disable libvirt domain autostart on host startup.", Fields: map[string]core.Field{"name": core.StringField("Domain name or UUID", true, 128), "enabled": {Type: "boolean", Required: true}}, Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
			return b.Autostart(ctx, in.String("name"), in.Bool("enabled"), out)
		}},
		core.Tool{Name: "kvm_define", Title: "Define KVM domain", Description: "Define or replace a persistent KVM domain from an existing XML file under libvirtXMLRoot (default vmRoot). Validates with libvirt; does not start the domain. XML references and host devices are trusted administrative configuration.", Destructive: true, Fields: map[string]core.Field{"file": core.StringField("Absolute path of an existing domain XML file", true, 4096)}, Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
			return b.Define(ctx, in.String("file"), out)
		}},
	)
	return r.AddModule(core.Module{ID: "machines", Title: "Machine management", Available: true}, tools...)
}
