package declarative

import (
	"context"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"server-man/internal/core"
	"server-man/internal/modules/shares"
)

type ShareStatus struct {
	ID            string  `json:"id"`
	Definition    Share   `json:"definition"`
	Configuration string  `json:"configuration"`
	Issues        []Issue `json:"issues"`
}

func (c Coordinator) ShareStatuses(ctx context.Context) (any, error) {
	snapshot, err := c.Store.Read()
	servers := map[string]map[string]any{}
	for _, p := range []string{"samba", "nfs"} {
		available := false
		discoveryAvailable := p != "samba"
		if adapter, ok := c.Adapters[p].(*ShareAdapter); ok && adapter != nil {
			if state, err := adapter.inspect(ctx, []shares.Definition{}); err == nil {
				available = state.Available
				if p == "samba" {
					discoveryAvailable = state.DiscoveryAvailable
				}
			}
		}
		servers[p] = map[string]any{"available": available, "discoveryAvailable": discoveryAvailable}
	}
	if os.IsNotExist(err) {
		return map[string]any{"configured": false, "shares": []ShareStatus{}, "servers": servers}, nil
	}
	if err != nil {
		return nil, core.Unavailable("Configuration is unavailable or invalid")
	}
	health := c.Preflight(ctx, snapshot.Config, false)
	states := map[string]Observation{}
	failures := map[string]bool{}
	items := []ShareStatus{}
	// Native fragments are per protocol; inspect once rather than once per share.
	for _, id := range slices.Sorted(maps.Keys(snapshot.Config.Shares)) {
		definition := snapshot.Config.Shares[id]
		item := ShareStatus{ID: id, Definition: definition, Configuration: "unknown", Issues: []Issue{}}
		for _, issue := range health.Issues {
			if issue.Resource == "share/"+id {
				item.Issues = append(item.Issues, issue)
			}
		}
		obs, checked := states[definition.Protocol]
		if !checked && !failures[definition.Protocol] {
			if adapter := c.Adapters[definition.Protocol]; adapter != nil {
				var err error
				obs, err = adapter.Inspect(ctx, snapshot.Config, "share/"+id)
				if err != nil {
					failures[definition.Protocol] = true
				} else {
					states[definition.Protocol] = obs
					checked = true
				}
			}
		}
		if checked {
			item.Configuration = "pending"
			if obs.Matches {
				item.Configuration = "published"
				if !definition.Enabled {
					item.Configuration = "unpublished"
				}
			}
		}
		items = append(items, item)
	}
	return map[string]any{"configured": true, "revision": snapshot.Revision, "shares": items, "health": health, "servers": servers}, nil
}
func (c Coordinator) SetSharePublished(ctx context.Context, id string, published bool) (Journal, error) {
	if !validID(id) {
		return Journal{}, core.Invalid("invalid share ID")
	}
	snapshot, err := c.Store.Read()
	if err != nil {
		return Journal{}, core.Unavailable("Configuration is unavailable or invalid")
	}
	definition, ok := snapshot.Config.Shares[id]
	if !ok {
		return Journal{}, core.Invalid("share does not exist")
	}
	definition.Enabled = published
	snapshot.Config.Shares[id] = definition
	return c.SaveApply(ctx, snapshot.Config, snapshot.Revision, "now")
}
func RegisterShareStatus(r *core.Registry, c Coordinator) error {
	return r.AddModule(core.Module{ID: "managed_shares", Title: "Configured file shares", Available: true},
		core.Tool{Name: "managed_shares", Title: "Configured file shares", Description: "Read authoritative share definitions and protocol publication state without changing host daemons.", ReadOnly: true, Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) { return c.ShareStatuses(ctx) }},
		core.Tool{Name: "managed_directory_browse", Title: "Browse directories", Description: "List accessible directory paths for choosing a share or storage path without showing file contents.", ReadOnly: true, Fields: map[string]core.Field{"path": core.StringField("Directory path to inspect", false, 4096)}, Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
			target := in.String("path")
			if target == "" {
				target = "/"
			}
			clean := filepath.Clean(target)
			if !filepath.IsAbs(clean) {
				clean = "/" + clean
			}
			entries, err := os.ReadDir(clean)
			if err != nil {
				return nil, core.Unavailable("Directory cannot be read: " + err.Error())
			}
			dirs := []string{}
			files := []string{}
			for _, entry := range entries {
				if len(dirs)+len(files) >= 256 {
					break
				}
				name := entry.Name()
				if strings.HasPrefix(name, ".") {
					continue
				}
				if entry.IsDir() {
					dirs = append(dirs, name)
				} else {
					files = append(files, name)
				}
			}
			slices.Sort(dirs)
			slices.Sort(files)
			parent := filepath.Dir(clean)
			if clean == "/" {
				parent = "/"
			}
			return map[string]any{
				"path":        clean,
				"parent":      parent,
				"directories": dirs,
				"files":       files,
			}, nil
		}},
		core.Tool{Name: "managed_share_publish", Title: "Publish or unpublish share", Description: "Persist the share's publication toggle and apply the authoritative protocol set. Does not change shared data, accounts or host daemon enablement.", Destructive: true, Fields: map[string]core.Field{"share": core.StringField("Share ID", true, 63), "published": {Type: "boolean", Required: true}}, Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
			j, err := c.SetSharePublished(ctx, in.String("share"), in.Bool("published"))
			if err != nil {
				return nil, managedError(err)
			}
			return j, nil
		}})
}
