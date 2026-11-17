package shares

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"

	"server-man/internal/core"
)

// NormalizeDefinitions validates typed native settings once for both the central
// model adapter and the root helper. Raw native option strings are never accepted.
func NormalizeDefinitions(protocol string, definitions []Definition, checkPaths bool) ([]Definition, error) {
	if protocol != "samba" && protocol != "nfs" {
		return nil, core.Invalid("unsupported share protocol")
	}
	if len(definitions) > 64 {
		return nil, core.Invalid("share limit exceeded")
	}
	names, paths := map[string]bool{}, map[string]bool{}
	result := []Definition{}
	for _, definition := range definitions {
		d, err := validate(protocol, definition, checkPaths)
		if err != nil {
			return nil, err
		}
		if names[d.Name] || (protocol == "nfs" && paths[d.Path]) {
			return nil, core.Invalid("duplicate share name or NFS export path")
		}
		names[d.Name] = true
		paths[d.Path] = true
		result = append(result, d)
	}
	slices.SortFunc(result, func(a, b Definition) int { return strings.Compare(a.Name, b.Name) })
	return result, nil
}

// ReplaceDefinitions publishes the entire authoritative protocol set. Existing
// metadata is read only for ownership verification, rollback and targeted removal.
func (b *Backend) ReplaceDefinitions(ctx context.Context, protocol, revision string, definitions []Definition, out io.Writer) (any, error) {
	next, err := NormalizeDefinitions(protocol, definitions, true)
	if err != nil {
		return nil, err
	}
	old, err := readFile(b.file(protocol), true)
	if err != nil {
		return nil, errors.New("managed share output is inaccessible")
	}
	if old.revision() != revision {
		return nil, &core.Error{Code: "conflict", Message: "Managed share output changed; recheck configuration", Status: 409}
	}
	before, err := load(protocol, old)
	if err != nil {
		return nil, err
	}
	return b.publishDefinitions(ctx, protocol, old, before, next, before, next, out)
}

// ValidateDefinitions exercises the same candidate/conflict checks as apply,
// using temporary native config files without publishing or reloading daemons.
func (b *Backend) ValidateDefinitions(ctx context.Context, protocol string, definitions []Definition) error {
	next, err := NormalizeDefinitions(protocol, definitions, true)
	if err != nil {
		return err
	}
	old, err := readFile(b.file(protocol), true)
	if err != nil {
		return err
	}
	before, err := load(protocol, old)
	if err != nil {
		return err
	}
	_, err = b.definitionsTransaction(ctx, protocol, old, before, next, before, next, io.Discard, false)
	return err
}

type DefinitionState struct {
	Revision           string `json:"revision"`
	Matches            bool   `json:"matches"`
	RuntimeMatches     bool   `json:"runtimeMatches"`
	Available          bool   `json:"available"`
	DiscoveryAvailable bool   `json:"discoveryAvailable"`
	Reason             string `json:"reason,omitempty"`
}

func (b *Backend) InspectDefinitions(ctx context.Context, protocol string, definitions []Definition) (DefinitionState, error) {
	expected, err := NormalizeDefinitions(protocol, definitions, false)
	if err != nil {
		return DefinitionState{}, err
	}
	file, err := readFile(b.file(protocol), true)
	if err != nil {
		return DefinitionState{}, err
	}
	if _, err = load(protocol, file); err != nil {
		return DefinitionState{}, err
	}
	desired, err := render(protocol, expected)
	if err != nil {
		return DefinitionState{}, err
	}
	result := DefinitionState{Revision: file.revision(), Matches: bytes.Equal(file.data, desired) || (!file.exists && len(expected) == 0), RuntimeMatches: true}
	inventory, err := b.List(ctx, protocol)
	if err != nil {
		return result, err
	}
	for _, backend := range inventory.Backends {
		result.Available = backend.CanManage
		result.DiscoveryAvailable = backend.DiscoveryAvailable
		result.Reason = backend.Reason
		if backend.SetupRequired {
			result.Reason = "Native share server setup is required"
		}
	}
	if !result.Available {
		result.RuntimeMatches = false
	}
	for _, want := range expected {
		found := false
		for _, actual := range inventory.Shares {
			if actual.Managed && actual.Name == want.Name {
				found = true
				if !actual.Configured || protocol == "nfs" && actual.Runtime != "exported" {
					result.RuntimeMatches = false
				}
			}
		}
		if !found {
			result.RuntimeMatches = false
		}
	}
	return result, nil
}
func decodeDefinitions(raw string) ([]Definition, error) {
	var definitions []Definition
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&definitions) != nil {
		return nil, core.Invalid("invalid share definitions")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, core.Invalid("expected one share definition array")
	}
	return definitions, nil
}

// RegisterDefinitions is helper-only. Application transports use central config
// operations rather than exposing a second native configuration write endpoint.
func (b *Backend) RegisterDefinitions(r *core.Registry) error {
	fields := func() map[string]core.Field {
		return map[string]core.Field{"protocol": core.EnumField("Share protocol", true, "samba", "nfs"), "definitions": core.StringField("Typed native definitions as JSON", true, 256<<10)}
	}
	applyFields := fields()
	applyFields["revision"] = core.StringField("Observed native output revision", true, 64)
	return r.AddModule(core.Module{ID: "share_definitions", Title: "Owned native share definitions", Available: true},
		core.Tool{Name: "shares_definitions_inspect", Title: "Inspect owned share definitions", ReadOnly: true, Fields: fields(), Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
			defs, err := decodeDefinitions(in.String("definitions"))
			if err != nil {
				return nil, err
			}
			return b.InspectDefinitions(ctx, in.String("protocol"), defs)
		}},
		core.Tool{Name: "shares_definitions_validate", Title: "Validate native share candidates", ReadOnly: true, Fields: fields(), Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
			defs, err := decodeDefinitions(in.String("definitions"))
			if err != nil {
				return nil, err
			}
			if err = b.ValidateDefinitions(ctx, in.String("protocol"), defs); err != nil {
				return nil, err
			}
			return map[string]bool{"valid": true}, nil
		}},
		core.Tool{Name: "shares_definitions_apply", Title: "Apply authoritative share definitions", Destructive: true, Fields: applyFields, Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
			defs, err := decodeDefinitions(in.String("definitions"))
			if err != nil {
				return nil, err
			}
			rev, err := b.ReplaceDefinitions(ctx, in.String("protocol"), in.String("revision"), defs, out)
			if err != nil {
				return nil, err
			}
			return map[string]any{"revision": rev}, nil
		}})
}
