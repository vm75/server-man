package declarative

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"server-man/internal/core"
)

// EditResource updates typed desired data only. Native operations remain in the
// coordinator, and every edit validates the complete resulting configuration.
func (c Coordinator) EditResource(kind, id, operation, value, revision string) (Config, error) {
	if !validID(id) {
		return Config{}, core.Invalid("invalid resource ID")
	}
	snapshot, err := c.Store.Read()
	if os.IsNotExist(err) {
		snapshot = Snapshot{Config: Config{Version: 1}, Revision: "missing"}
	} else if err != nil {
		return Config{}, core.Unavailable("Repair the invalid configuration before using definition forms")
	}
	if revision != snapshot.Revision {
		return Config{}, ErrConflict
	}
	cfg := snapshot.Config
	// All maps come from this fresh file read, so edits cannot mutate an in-flight snapshot.
	if cfg.Services == nil {
		cfg.Services = map[string]Service{}
	}
	if cfg.Storage == nil {
		cfg.Storage = map[string]Storage{}
	}
	if cfg.Shares == nil {
		cfg.Shares = map[string]Share{}
	}
	if cfg.Bindings == nil {
		cfg.Bindings = map[string]Binding{}
	}
	if cfg.Secrets == nil {
		cfg.Secrets = map[string]string{}
	}
	if operation != "save" && operation != "remove" {
		return Config{}, core.Invalid("unsupported resource operation")
	}
	decode := func(dest any) error {
		d := json.NewDecoder(strings.NewReader(value))
		d.DisallowUnknownFields()
		if strings.TrimSpace(value) == "null" || d.Decode(dest) != nil {
			return core.Invalid("definition has unknown fields or incorrect field types")
		}
		var extra any
		if d.Decode(&extra) != io.EOF {
			return core.Invalid("expected one typed resource definition")
		}
		return nil
	}
	switch kind {
	case "service":
		if operation == "remove" {
			if _, ok := cfg.Services[id]; !ok {
				return Config{}, core.Invalid("service does not exist")
			}
			delete(cfg.Services, id)
		} else {
			var v Service
			if err = decode(&v); err != nil {
				return Config{}, err
			}
			if cfg.Storage == nil {
				cfg.Storage = map[string]Storage{}
			}
			for cName, cVal := range v.Containers {
				for i, m := range cVal.Mounts {
					if m.Path != "" {
						cleanPath := filepath.Clean(m.Path)
						mKind := m.Kind
						if mKind != "file" && mKind != "directory" {
							if fi, err := os.Stat(cleanPath); err == nil && !fi.IsDir() {
								mKind = "file"
							} else {
								mKind = "directory"
							}
						}
						if m.Storage == "" || cfg.Storage[m.Storage].Path != cleanPath {
							m.Storage = ""
							for sID, st := range cfg.Storage {
								if st.Path == cleanPath && (mKind == "" || st.Kind == mKind) {
									m.Storage = sID
									break
								}
							}
							if m.Storage == "" {
								base := id + "-" + filepath.Base(cleanPath)
								base = strings.Map(func(r rune) rune {
									if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
										return r
									}
									if r >= 'A' && r <= 'Z' {
										return r + ('a' - 'A')
									}
									return '-'
								}, base)
								storageID := strings.Trim(base, "-")
								if storageID == "" {
									storageID = id + "-storage"
								}
								candidate := storageID
								for counter := 1; ; counter++ {
									if existing, ok := cfg.Storage[candidate]; !ok || existing.Path == cleanPath {
										storageID = candidate
										break
									}
									candidate = fmt.Sprintf("%s-%d", storageID, counter)
								}
								m.Storage = storageID
							}
						}
						if _, ok := cfg.Storage[m.Storage]; !ok {
							cfg.Storage[m.Storage] = Storage{
								Path:     cleanPath,
								External: filepath.IsAbs(cleanPath),
								Kind:     mKind,
							}
						}
						cVal.Mounts[i] = m
					}
				}
				v.Containers[cName] = cVal
			}
			cfg.Services[id] = v
		}
	case "storage":
		if operation == "remove" {
			if _, ok := cfg.Storage[id]; !ok {
				return Config{}, core.Invalid("storage does not exist")
			}
			delete(cfg.Storage, id)
		} else {
			var v Storage
			if err = decode(&v); err != nil {
				return Config{}, err
			}
			cfg.Storage[id] = v
		}
	case "share":
		if operation == "remove" {
			if _, ok := cfg.Shares[id]; !ok {
				return Config{}, core.Invalid("share does not exist")
			}
			delete(cfg.Shares, id)
		} else {
			var v Share
			if err = decode(&v); err != nil {
				return Config{}, err
			}
			if v.Path != "" {
				cleanPath := filepath.Clean(v.Path)
				if v.Storage == "" {
					for sID, st := range cfg.Storage {
						if st.Kind != "file" && st.Path == cleanPath {
							v.Storage = sID
							break
						}
					}
					if v.Storage == "" {
						storageID := id
						if existing, ok := cfg.Storage[storageID]; ok && existing.Path != cleanPath {
							storageID = id + "-storage"
						}
						v.Storage = storageID
					}
				}
				if _, ok := cfg.Storage[v.Storage]; !ok {
					cfg.Storage[v.Storage] = Storage{
						Path:     cleanPath,
						External: filepath.IsAbs(cleanPath),
						Kind:     "directory",
					}
				}
			}
			cfg.Shares[id] = v
		}
	case "binding":
		if operation == "remove" {
			if _, ok := cfg.Bindings[id]; !ok {
				return Config{}, core.Invalid("binding does not exist")
			}
			delete(cfg.Bindings, id)
		} else {
			var v Binding
			if err = decode(&v); err != nil {
				return Config{}, err
			}
			cfg.Bindings[id] = v
		}
	case "secret":
		if operation == "remove" {
			if _, ok := cfg.Secrets[id]; !ok {
				return Config{}, core.Invalid("private file reference does not exist")
			}
			delete(cfg.Secrets, id)
		} else {
			var v string
			if err = decode(&v); err != nil {
				return Config{}, err
			}
			cfg.Secrets[id] = v
		}
	default:
		return Config{}, core.Invalid("unsupported resource kind")
	}
	if err = cfg.Validate(); err != nil {
		return Config{}, core.Invalid("%s", err)
	}
	return cfg, nil
}
func RegisterEditing(r *core.Registry, c Coordinator) error {
	fields := func() map[string]core.Field {
		return map[string]core.Field{"kind": core.EnumField("Resource kind", true, "service", "storage", "share", "binding", "secret"), "id": core.StringField("Stable resource ID", true, 63), "operation": core.EnumField("Definition operation", true, "save", "remove"), "definition": core.StringField("Typed definition as JSON; omitted for removal", false, 256<<10), "revision": core.StringField("Current central configuration revision or missing", true, 64)}
	}
	saveFields := fields()
	saveFields["mode"] = core.EnumField("Apply now or publish for next start", true, "now", "next_start")
	return r.AddModule(core.Module{ID: "managed_edit", Title: "Managed definition editing", Available: true},
		core.Tool{Name: "managed_config_model", Title: "Configuration form data", Description: "Read typed private desired definitions for editing. Disabled with management writes; never reads referenced secret files.", ReadOnly: true, Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
			if !r.AllowWrites {
				return nil, &core.Error{Code: "writes_disabled", Message: "Configuration editing is disabled with management writes.", Status: 403}
			}
			snapshot, err := c.Store.Read()
			if os.IsNotExist(err) {
				return Snapshot{Config: Config{Version: 1}, Revision: "missing"}, nil
			}
			if err != nil {
				return nil, core.Unavailable("Configuration is invalid or inaccessible; use the configuration recovery editor")
			}
			return snapshot, nil
		}},
		core.Tool{Name: "managed_resource_plan", Title: "Review definition change", Description: "Validate a typed definition edit and calculate affected resources before saving. Does not modify configuration or workloads.", ReadOnly: true, Fields: fields(), Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
			cfg, err := c.EditResource(in.String("kind"), in.String("id"), in.String("operation"), in.String("definition"), in.String("revision"))
			if err != nil {
				return nil, managedError(err)
			}
			p, err := c.Preview(ctx, cfg, in.String("revision"))
			if err != nil {
				return nil, managedError(err)
			}
			return p, nil
		}},
		core.Tool{Name: "managed_resource_save", Title: "Save definition change", Description: "Save a revision-checked typed definition through the authoritative apply workflow. Removing definitions retains persistent data where supported; unsafe removals fail explicitly.", Destructive: true, Fields: saveFields, Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
			cfg, err := c.EditResource(in.String("kind"), in.String("id"), in.String("operation"), in.String("definition"), in.String("revision"))
			if err != nil {
				return nil, managedError(err)
			}
			j, err := c.SaveApply(ctx, cfg, in.String("revision"), in.String("mode"))
			if err != nil {
				return nil, managedError(err)
			}
			return j, nil
		}})
}
