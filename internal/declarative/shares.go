package declarative

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"slices"
	"strings"

	"server-man/internal/core"
	"server-man/internal/modules/shares"
)

// PrivilegedShareCaller is the existing narrow one-shot helper boundary, injected
// by assembly so domain code never imports the privilege-routing implementation.
type PrivilegedShareCaller interface {
	Call(context.Context, string, core.Input, io.Writer) (any, error)
}
type ShareAdapter struct {
	Store    Store
	Protocol string
	Native   *shares.Backend
	Helper   PrivilegedShareCaller
}

func (s *ShareAdapter) definitions(c Config, checkPaths bool) ([]shares.Definition, error) {
	definitions := []shares.Definition{}
	for id, definition := range c.Shares {
		if definition.Protocol != s.Protocol || !definition.Enabled {
			continue
		}
		security := definition.Security
		if security == "" && s.Protocol == "nfs" {
			security = "sys"
		}
		squash := s.Protocol == "nfs"
		if definition.RootSquash != nil {
			squash = *definition.RootSquash
		}
		users := []string(nil)
		if s.Protocol == "samba" && !definition.Guest {
			account, err := shares.CurrentAccount()
			if err != nil {
				return nil, err
			}
			users = []string{account}
		}
		definitions = append(definitions, shares.Definition{Name: id, Path: s.Store.Resolve(c.Storage[definition.Storage].Path), ReadOnly: definition.ReadOnly, Guest: definition.Guest, Browseable: definition.Browseable, Users: users, Clients: slices.Clone(definition.Clients), RootSquash: squash, Security: security})
	}
	return shares.NormalizeDefinitions(s.Protocol, definitions, checkPaths)
}
func (s *ShareAdapter) inspect(ctx context.Context, definitions []shares.Definition) (shares.DefinitionState, error) {
	if s.Helper == nil {
		return s.Native.InspectDefinitions(ctx, s.Protocol, definitions)
	}
	data, err := json.Marshal(definitions)
	if err != nil {
		return shares.DefinitionState{}, err
	}
	result, err := s.Helper.Call(ctx, "shares_definitions_inspect", core.Input{"protocol": s.Protocol, "definitions": string(data)}, io.Discard)
	if err != nil {
		return shares.DefinitionState{}, err
	}
	data, err = json.Marshal(result)
	if err != nil {
		return shares.DefinitionState{}, err
	}
	var state shares.DefinitionState
	if json.Unmarshal(data, &state) != nil {
		return state, errors.New("invalid privileged share inspection")
	}
	return state, nil
}
func (s *ShareAdapter) Validate(ctx context.Context, c Config, key string) error {
	if err := c.Validate(); err != nil {
		return err
	}
	kind, id, ok := strings.Cut(key, "/")
	if !ok || kind != "share" || c.Shares[id].Protocol != s.Protocol {
		return errors.New("invalid share resource")
	}
	definitions, err := s.definitions(c, true)
	if err != nil {
		return err
	}
	state, err := s.inspect(ctx, definitions)
	if err != nil {
		return errors.New("native share configuration cannot be safely inspected")
	}
	if !state.Available {
		return errors.New("native share dependencies, permissions or setup are unavailable")
	}
	if s.Helper == nil {
		return s.Native.ValidateDefinitions(ctx, s.Protocol, definitions)
	}
	data, err := json.Marshal(definitions)
	if err != nil {
		return err
	}
	_, err = s.Helper.Call(ctx, "shares_definitions_validate", core.Input{"protocol": s.Protocol, "definitions": string(data)}, io.Discard)
	return err
}
func (s *ShareAdapter) Inspect(ctx context.Context, c Config, key string) (Observation, error) {
	definitions, err := s.definitions(c, false)
	if err != nil {
		return Observation{}, err
	}
	state, err := s.inspect(ctx, definitions)
	if err != nil {
		return Observation{}, err
	}
	return Observation{Registered: state.Matches, Matches: state.Matches && state.RuntimeMatches, ActiveMatches: true}, nil
}
func (s *ShareAdapter) Apply(ctx context.Context, previous, desired Config, key, mode string) error {
	definitions, err := s.definitions(desired, true)
	if err != nil {
		return err
	}
	state, err := s.inspect(ctx, definitions)
	if err != nil {
		return err
	}
	if s.Helper == nil {
		_, err = s.Native.ReplaceDefinitions(ctx, s.Protocol, state.Revision, definitions, io.Discard)
		return err
	}
	data, err := json.Marshal(definitions)
	if err != nil {
		return err
	}
	_, err = s.Helper.Call(ctx, "shares_definitions_apply", core.Input{"protocol": s.Protocol, "definitions": string(data), "revision": state.Revision, "confirm": true}, io.Discard)
	return err
}

// A protocol shares one fragment and validator, so probe it once per config.
func (s *ShareAdapter) ConfigurationPrerequisites(ctx context.Context, c Config) []Issue {
	ids := []string{}
	for _, id := range slices.Sorted(maps.Keys(c.Shares)) {
		if c.Shares[id].Protocol == s.Protocol {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	if err := s.Validate(ctx, c, "share/"+ids[0]); err != nil {
		return []Issue{{"error", "backend/" + s.Protocol, "share", "share_setup", err.Error(), "Check native share packages/setup, directory permissions and access identities; setup/install are explicit host operations."}}
	}
	return nil
}
func (s *ShareAdapter) Prerequisites(context.Context) []Issue { return nil }
