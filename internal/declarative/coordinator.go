package declarative

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"server-man/internal/config"
	"slices"
	"strings"
	"time"
)

// Adapter operations must affect only the selected resource and preserve data.
// Apply publishes definitions; it must not start a stopped resource. mode is
// "now" or "next_start". Inspect must verify native state rather than trust a cache.
type Adapter interface {
	Validate(context.Context, Config, string) error
	Inspect(context.Context, Config, string) (Observation, error)
	Apply(context.Context, Config, Config, string, string) error
}

// A backend with native state-dependent edit constraints can veto a candidate
// before the authoritative YAML is replaced. It must not mutate the backend.
type transitionValidator interface {
	ValidateTransition(context.Context, Config, Config, string) error
}

func validateNativeTransition(ctx context.Context, a Adapter, previous, desired Config, key string) error {
	validator, ok := a.(transitionValidator)
	if !ok {
		return nil
	}
	if err := validator.ValidateTransition(ctx, previous, desired, key); err != nil {
		if errors.Is(err, errIncusStoppedUpdate) {
			return fmt.Errorf("%s: %w", key, errIncusStoppedUpdate)
		}
		return fmt.Errorf("%s: native transition could not be verified", key)
	}
	return nil
}
type Observation struct {
	State         string                  `json:"state,omitempty"`
	Members       []ContainerMemberStatus `json:"members,omitempty"`
	Registered    bool                    `json:"registered"`
	ActiveMatches bool                    `json:"activeMatches"`
	Active        bool                    `json:"active"`
	Matches       bool                    `json:"matches"`
	Issues        []Issue                 `json:"issues,omitempty"`
}
type Change struct {
	Resource    string `json:"resource"`
	Backend     string `json:"backend"`
	Operation   string `json:"operation"`
	Fingerprint string `json:"fingerprint"`
}
type Plan struct {
	RestartRequired bool     `json:"restartRequired"`
	BaseRevision    string   `json:"baseRevision"`
	DesiredRevision string   `json:"desiredRevision"`
	Changes         []Change `json:"changes"`
	Files           Health   `json:"files"`
}
type ResourceResult struct {
	Fingerprint string `json:"fingerprint"`
	State       string `json:"state"`
	Message     string `json:"message,omitempty"`
}
type Journal struct {
	Superseded      *Journal                  `json:"superseded,omitempty"`
	Version         int                       `json:"version"`
	BaseRevision    string                    `json:"baseRevision"`
	DesiredRevision string                    `json:"desiredRevision"`
	Stage           string                    `json:"stage"`
	Mode            string                    `json:"mode"`
	UpdatedAt       time.Time                 `json:"updatedAt"`
	Resources       map[string]ResourceResult `json:"resources"`
}
type Coordinator struct {
	RunningServer *config.Config
	HealthCache   *HealthCache
	Store         Store
	Adapters      map[string]Adapter
}

func backend(c Config, key string) string {
	kind, id, _ := strings.Cut(key, "/")
	if kind == "share" {
		return c.Shares[id].Protocol
	}
	s := c.Services[id]
	if s.Kind == "container" {
		return s.Engine + "/" + s.Backend
	}
	return s.Backend
}
func resourceFingerprint(c Config, key string) string {
	kind, id, _ := strings.Cut(key, "/")
	// Include resolved references in the definition hash so rebinding storage or
	// networking invalidates affected definitions as well as direct edits.
	refs := map[string]any{}
	var definition any
	addStorage := func(id string) { refs["storage/"+id] = c.Storage[id] }
	if kind == "share" {
		s, ok := c.Shares[id]
		if !ok {
			return ""
		}
		definition = s
		addStorage(s.Storage)
	} else {
		s, ok := c.Services[id]
		if !ok {
			return ""
		}
		definition = s
		for _, reference := range serviceDataReferences(s) {
			kind, id, _ := strings.Cut(reference, "/")
			if kind == "storage" {
				addStorage(id)
			} else {
				refs[reference] = c.Secrets[id]
			}
		}
		if s.VM != nil {
			for _, b := range []string{s.VM.Network, s.VM.Pool, s.VM.Project} {
				if b != "" {
					refs["binding/"+b] = c.Bindings[b]
				}
			}
		}
	}
	bytes, _ := json.Marshal([]any{definition, refs})
	return digest(bytes)
}
func orderedResources(c Config) []string {
	ids := make([]string, 0, len(c.Services))
	for id := range c.Services {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	result := []string{}
	seen := map[string]bool{}
	var visit func(string)
	visit = func(id string) {
		if seen[id] {
			return
		}
		seen[id] = true
		deps := slices.Clone(c.Services[id].DependsOn)
		slices.Sort(deps)
		for _, dep := range deps {
			visit(dep)
		}
		result = append(result, "service/"+id)
	}
	for _, id := range ids {
		visit(id)
	}
	ids = nil
	for id := range c.Shares {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		result = append(result, "share/"+id)
	}
	return result
}
func (c Coordinator) prepare(candidate Config, expected string) (Config, Config, Plan, error) {
	raw, revision, err := c.Store.ReadRaw()
	if os.IsNotExist(err) {
		revision = "missing"
	} else if err != nil {
		return Config{}, Config{}, Plan{}, err
	}
	if expected != revision {
		return Config{}, Config{}, Plan{}, ErrConflict
	}
	previous := previousConfiguration(raw)
	desired, err := PrepareIdentities(candidate, previous)
	if err != nil {
		return Config{}, Config{}, Plan{}, err
	}
	for id, old := range previous.Services {
		if next, ok := desired.Services[id]; ok && (old.Kind != next.Kind || backend(previous, "service/"+id) != backend(desired, "service/"+id)) {
			return Config{}, Config{}, Plan{}, errors.New("changing an existing workload kind or backend requires a new definition")
		}
	}
	for id, old := range previous.Shares {
		if next, ok := desired.Shares[id]; ok && old.Protocol != next.Protocol {
			return Config{}, Config{}, Plan{}, errors.New("changing a share protocol requires an explicit new definition")
		}
	}
	bytes, err := Encode(desired)
	if err != nil {
		return Config{}, Config{}, Plan{}, err
	}
	plan := Plan{BaseRevision: revision, DesiredRevision: digest(bytes), Changes: []Change{}, Files: c.Store.CheckFiles(desired)}
	// Removed dependants precede dependencies. Updated resources follow dependency order.
	removed := orderedResources(previous)
	slices.Reverse(removed)
	for _, key := range removed {
		if resourceFingerprint(desired, key) == "" {
			plan.Changes = append(plan.Changes, Change{key, backend(previous, key), "remove", ""})
		}
	}
	for _, key := range orderedResources(desired) {
		fp := resourceFingerprint(desired, key)
		old := resourceFingerprint(previous, key)
		op := "update"
		if old == "" {
			op = "create"
		}
		if old != fp {
			plan.Changes = append(plan.Changes, Change{key, backend(desired, key), op, fp})
		}
	}
	plan.RestartRequired = c.serverRestartRequired(desired)
	if plan.RestartRequired && len(plan.Changes) > 0 {
		return Config{}, Config{}, Plan{}, errors.New("save server settings separately and restart server-man before changing managed resources")
	}
	return previous, desired, plan, nil
}
func (c Coordinator) Preview(ctx context.Context, candidate Config, expected string) (Plan, error) {
	previous, desired, plan, err := c.prepare(candidate, expected)
	if err != nil {
		return Plan{}, err
	}
	for _, change := range plan.Changes {
		if change.Operation == "remove" {
			continue
		}
		if a := c.Adapters[change.Backend]; a != nil {
			if err = a.Validate(ctx, desired, change.Resource); err != nil {
				return Plan{}, fmt.Errorf("%s: native definition validation failed", change.Resource)
			}
			if err = validateNativeTransition(ctx, a, previous, desired, change.Resource); err != nil {
				return Plan{}, err
			}
		}
	}
	return plan, nil
}

// SaveApply is called only inside the shared mutation job lock. Desired state is
// retained after failure. A pending/failed journal never counts as successful apply.
func (c Coordinator) SaveApply(ctx context.Context, candidate Config, expected, mode string) (Journal, error) {
	return c.saveApply(ctx, candidate, expected, mode, false)
}

func (c Coordinator) saveApply(ctx context.Context, candidate Config, expected, mode string, revert bool) (Journal, error) {
	if err := c.checkMutationReady(); err != nil {
		return Journal{}, err
	}
	if mode != "now" && mode != "next_start" {
		return Journal{}, errors.New("unsupported apply mode")
	}
	previous, desired, plan, err := c.prepare(candidate, expected)
	if err != nil {
		return Journal{}, err
	}
	existing, err := c.ReadJournal()
	if err != nil && !os.IsNotExist(err) {
		return Journal{}, err
	}
	if err == nil && existing.Stage != "complete" && existing.Stage != "aborted" {
		if !revert {
			return existing, errors.New("an incomplete apply requires inspection and retry before another save")
		}
		if err = c.validateSupersession(ctx, existing, previous, desired, expected); err != nil {
			return existing, err
		}
	}
	// Verify unchanged definitions too: a copied manifest or external drift must
	// not be treated as applied simply because its YAML did not change.
	planned := map[string]bool{}
	for _, change := range plan.Changes {
		planned[change.Resource] = true
	}
	if !plan.RestartRequired {
		for _, key := range orderedResources(desired) {
			if planned[key] {
				continue
			}
			a := c.Adapters[backend(desired, key)]
			matches := false
			if a != nil {
				obs, e := a.Inspect(ctx, desired, key)
				matches = e == nil && obs.Matches && (mode == "next_start" || !obs.Active || obs.ActiveMatches) && artifactPublicationMatchesDesired(a, desired, key)
			}
			if !matches {
				plan.Changes = append(plan.Changes, Change{key, backend(desired, key), "update", resourceFingerprint(desired, key)})
			}
		}
	}
	// Rebuild update ordering after adding drifted unchanged dependencies.
	ordered := []Change{}
	updates := map[string]Change{}
	for _, change := range plan.Changes {
		if change.Operation == "remove" {
			ordered = append(ordered, change)
		} else {
			updates[change.Resource] = change
		}
	}
	for _, key := range orderedResources(desired) {
		if change, ok := updates[key]; ok {
			ordered = append(ordered, change)
		}
	}
	plan.Changes = ordered
	// Validate before committing intent or configuration; missing adapters can be
	// represented in next-start desired state, but cannot claim successful apply.
	for _, change := range plan.Changes {
		if change.Operation == "remove" {
			continue
		}
		if a := c.Adapters[change.Backend]; a != nil {
			if err = a.Validate(ctx, desired, change.Resource); err != nil {
				return Journal{}, fmt.Errorf("%s: native validation failed", change.Resource)
			}
			if err = validateNativeTransition(ctx, a, previous, desired, change.Resource); err != nil {
				return Journal{}, err
			}
		}
	}
	if err = ctx.Err(); err != nil {
		return Journal{}, err
	}
	j := Journal{Version: 1, BaseRevision: plan.BaseRevision, DesiredRevision: plan.DesiredRevision, Stage: "prepared", Mode: mode, Resources: map[string]ResourceResult{}}
	if revert && existing.Version == 1 && existing.Stage != "complete" && existing.Stage != "aborted" {
		j.Superseded = &existing
	}
	for _, change := range plan.Changes {
		j.Resources[change.Resource] = ResourceResult{Fingerprint: change.Fingerprint, State: "pending"}
	}
	if err = c.writeJournal(&j); err != nil {
		return j, err
	}
	saved, err := c.Store.Save(desired, expected)
	if err != nil {
		return j, err
	}
	j.DesiredRevision = saved.Revision
	j.Superseded = nil
	j.Stage = "saved"
	if err = c.writeJournal(&j); err != nil {
		return j, err
	}
	if mode == "now" && affectedFileIssues(desired, plan.Changes, plan.Files) {
		j.Stage = "blocked"
		if err = c.writeJournal(&j); err != nil {
			return j, err
		}
		return j, errors.New("configuration saved; required files or mounts block apply")
	}
	return c.apply(ctx, previous, desired, plan.Changes, j)
}
func (c Coordinator) apply(ctx context.Context, previous, desired Config, changes []Change, j Journal) (Journal, error) {
	for _, change := range changes {
		if err := ctx.Err(); err != nil {
			return j, err
		}
		a := c.Adapters[change.Backend]
		if a == nil {
			j.Stage = "blocked"
			j.Resources[change.Resource] = ResourceResult{change.Fingerprint, "blocked", "Backend adapter is unavailable."}
			if err := c.writeJournal(&j); err != nil {
				return j, err
			}
			return j, errors.New("configuration saved; backend unavailable")
		}
		if change.Operation != "remove" {
			observed, e := a.Inspect(ctx, desired, change.Resource)
			if e == nil && observed.Matches && (j.Mode == "next_start" || !observed.Active || observed.ActiveMatches) && artifactPublicationMatchesDesired(a, desired, change.Resource) {
				state := "registered"
				if observed.Active && !observed.ActiveMatches {
					state = "restart_required"
				}
				j.Resources[change.Resource] = ResourceResult{Fingerprint: change.Fingerprint, State: state}
				if err := c.writeJournal(&j); err != nil {
					return j, err
				}
				continue
			}
		}
		j.Stage = "applying"
		j.Resources[change.Resource] = ResourceResult{Fingerprint: change.Fingerprint, State: "applying"}
		if err := c.writeJournal(&j); err != nil {
			return j, err
		}
		state := "registered"
		err := a.Apply(ctx, previous, desired, change.Resource, j.Mode)
		if err == nil {
			var observed Observation
			if change.Operation == "remove" {
				if verifier, ok := a.(interface {
					VerifyRemoved(context.Context, Config, string) (bool, error)
				}); ok {
					observed.Matches, err = verifier.VerifyRemoved(ctx, previous, change.Resource)
				} else {
					observed, err = a.Inspect(ctx, desired, change.Resource)
				}
			} else {
				observed, err = a.Inspect(ctx, desired, change.Resource)
			}
			if err == nil && observed.Active && !observed.ActiveMatches {
				state = "restart_required"
				if j.Mode == "now" {
					err = errors.New("running workload has not loaded desired settings")
				}
			}
			if err == nil && !observed.Matches {
				err = errors.New("native definition does not match desired state")
			}
		}
		if err != nil {
			j.Stage = "failed"
			message, jobError := "Native application or verification failed; inspect backend state before retry.", "application failed"
			// Only known domain errors supply public detail. Native errors may
			// contain secret-derived output and must retain generic diagnostics.
			if errors.Is(err, errArtifactConflict) {
				message, jobError = artifactConflictRemedy, artifactConflictRemedy
			} else if errors.Is(err, errIncusStoppedUpdate) {
				// Native state may have changed after the pre-save guard. Preserve
				// the actionable reason in the durable journal, without backend output.
				message, jobError = errIncusStoppedUpdate.Error(), errIncusStoppedUpdate.Error()
			}
			j.Resources[change.Resource] = ResourceResult{change.Fingerprint, "failed", message}
			if e := c.writeJournal(&j); e != nil {
				return j, e
			}
			return j, fmt.Errorf("%s: %s", change.Resource, jobError)
		}
		j.Resources[change.Resource] = ResourceResult{Fingerprint: change.Fingerprint, State: state}
		if err = c.writeJournal(&j); err != nil {
			return j, err
		}
	}
	j.Stage = "complete"
	return j, c.writeJournal(&j)
}

// Retry inspects a durable transition; it never automatically starts workloads
// or blindly repeats resource removals after an interrupted native operation.
func (c Coordinator) Retry(ctx context.Context) (Journal, error) {
	if err := c.checkMutationReady(); err != nil {
		return Journal{}, err
	}
	j, err := c.ReadJournal()
	if err != nil {
		return j, err
	}
	raw, revision, err := c.Store.ReadRaw()
	if os.IsNotExist(err) {
		revision = "missing"
	} else if err != nil {
		return j, err
	}
	if j.Stage == "prepared" && revision == j.BaseRevision {
		if j.Superseded != nil {
			prior := *j.Superseded
			return prior, c.writeJournal(&prior)
		}
		j.Stage = "aborted"
		return j, c.writeJournal(&j) // intent was durable, config was not committed
	}
	if revision != j.DesiredRevision {
		return j, ErrConflict
	}
	desired, err := Parse(raw)
	if err != nil {
		return j, err
	}
	j.Superseded = nil
	if j.Stage == "complete" || j.Stage == "aborted" {
		return j, nil
	}
	previous := Config{Version: 1}
	if j.BaseRevision != "missing" {
		if !revisionPattern.MatchString(j.BaseRevision) {
			return j, errors.New("invalid journal base revision")
		}
		bytes, readErr := c.Store.ReadHistory(j.BaseRevision)
		if readErr != nil || digest(bytes) != j.BaseRevision {
			return j, errors.New("previous configuration snapshot is unavailable or damaged")
		}
		previous = previousConfiguration(bytes)
	}
	pendingChanges := []Change{}
	for resource := range j.Resources {
		op := "update"
		if resourceFingerprint(desired, resource) == "" {
			op = "remove"
		}
		pendingChanges = append(pendingChanges, Change{Resource: resource, Operation: op})
	}
	if j.Mode == "now" && affectedFileIssues(desired, pendingChanges, c.Store.CheckFiles(desired)) {
		return j, errors.New("required files or mounts still block apply")
	}
	changes := []Change{}
	for _, key := range orderedResources(desired) {
		result, ok := j.Resources[key]
		if !ok {
			continue
		}
		a := c.Adapters[backend(desired, key)]
		if a != nil {
			obs, e := a.Inspect(ctx, desired, key)
			if e == nil && obs.Matches && (j.Mode == "next_start" || !obs.Active || obs.ActiveMatches) && artifactPublicationMatchesDesired(a, desired, key) {
				state := "registered"
				if obs.Active && !obs.ActiveMatches {
					state = "restart_required"
				}
				j.Resources[key] = ResourceResult{result.Fingerprint, state, ""}
				continue
			}
			if err = a.Validate(ctx, desired, key); err != nil {
				return j, fmt.Errorf("%s: native validation failed", key)
			}
		}
		changes = append(changes, Change{key, backend(desired, key), "update", result.Fingerprint})
	}
	removed := orderedResources(previous)
	slices.Reverse(removed)
	for _, key := range removed {
		if resourceFingerprint(desired, key) != "" {
			continue
		}
		result, ok := j.Resources[key]
		if !ok {
			continue
		}
		a := c.Adapters[backend(previous, key)]
		if a == nil {
			return j, errors.New("removal backend unavailable")
		}
		obs, e := a.Inspect(ctx, previous, key)
		if e != nil {
			return j, errors.New("cannot establish removed resource state")
		}
		if obs.Registered || obs.Active {
			return j, errors.New("interrupted removal requires explicit inspection; automatic destructive replay refused")
		}
		j.Resources[key] = ResourceResult{Fingerprint: result.Fingerprint, State: "registered"}
	}
	return c.apply(ctx, previous, desired, changes, j)
}

// Boot policy and display metadata do not change container execution settings.
func runtimeFingerprint(c Config, key string) string {
	_, id, _ := strings.Cut(key, "/")
	s := c.Services[id]
	s.Enabled = false
	s.Name = ""
	s.Description = ""
	c.Services = maps.Clone(c.Services)
	c.Services[id] = s
	c.Storage = maps.Clone(c.Storage)
	for storageID, storage := range c.Storage {
		storage.Source = nil
		c.Storage[storageID] = storage
	}
	return resourceFingerprint(c, key)
}

// Unused storage still appears in health but does not block unrelated apply or
// unpublishing a share whose data is missing. Only affected live definitions need it.
func affectedFileIssues(c Config, changes []Change, health Health) bool {
	required := map[string]bool{}
	for _, change := range changes {
		if change.Operation == "remove" {
			continue
		}
		kind, id, _ := strings.Cut(change.Resource, "/")
		if kind == "share" && !c.Shares[id].Enabled {
			continue
		}
		required[change.Resource] = true
	}
	for _, issue := range health.Issues {
		if required[issue.Resource] && issue.Severity != "warning" {
			return true
		}
	}
	return false
}
