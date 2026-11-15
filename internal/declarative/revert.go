package declarative

import (
	"context"
	"errors"
	"os"
)

func (c Coordinator) revertCandidate(target string) (Config, error) {
	raw, err := c.Store.ReadHistory(target)
	if err != nil {
		return Config{}, err
	}
	cfg, err := Parse(raw)
	if err != nil {
		return Config{}, errors.New("selected history revision is not valid configuration")
	}
	return cfg, nil
}

// Revert selects historical desired state through the ordinary apply path; it
// never restores data or promises to reverse completed runtime/client effects.
func (c Coordinator) Revert(ctx context.Context, target, expected, mode string) (Journal, error) {
	cfg, err := c.revertCandidate(target)
	if err != nil {
		return Journal{}, err
	}
	return c.saveApply(ctx, cfg, expected, mode, true)
}

func (c Coordinator) PreviewRevert(ctx context.Context, target, expected string) (Plan, error) {
	cfg, err := c.revertCandidate(target)
	if err != nil {
		return Plan{}, err
	}
	previous, desired, _, err := c.prepare(cfg, expected)
	if err != nil {
		return Plan{}, err
	}
	prior, err := c.ReadJournal()
	if err != nil && !os.IsNotExist(err) {
		return Plan{}, err
	}
	if err == nil && prior.Stage != "complete" && prior.Stage != "aborted" {
		if err = c.validateSupersession(ctx, prior, previous, desired, expected); err != nil {
			return Plan{}, err
		}
	}
	return c.Preview(ctx, cfg, expected)
}

// An unfinished removal cannot disappear from tracking just because a revert
// selects an even older configuration that also lacks that resource.
func (c Coordinator) validateSupersession(ctx context.Context, prior Journal, current, desired Config, expected string) error {
	if prior.Stage == "prepared" || prior.Superseded != nil {
		return errors.New("inspect and retry the prepared transition before reverting")
	}
	if prior.DesiredRevision != expected {
		return ErrConflict
	}
	before := Config{Version: 1}
	if prior.BaseRevision != "missing" {
		raw, err := c.Store.ReadHistory(prior.BaseRevision)
		if err != nil {
			return err
		}
		before = previousConfiguration(raw)
	}
	for key := range prior.Resources {
		if resourceFingerprint(before, key) == "" || resourceFingerprint(current, key) != "" || resourceFingerprint(desired, key) != "" {
			continue
		}
		adapter := c.Adapters[backend(before, key)]
		if adapter == nil {
			return errors.New("revert cannot establish the state of a prior removal")
		}
		observed, err := adapter.Inspect(ctx, before, key)
		if err != nil || observed.Active || observed.Registered {
			return errors.New("revert would abandon an unresolved removal; select a revision restoring that resource or resolve its native state first")
		}
	}
	return nil
}
