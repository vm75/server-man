package declarative

import (
	"context"
	"errors"
	"fmt"
)

type LifecycleAdapter interface {
	Action(context.Context, Config, string, string) error
}

func (c Coordinator) ServiceAction(ctx context.Context, id, action string) error {
	if !validID(id) {
		return errors.New("invalid service ID")
	}
	if action != "start" && action != "stop" && action != "restart" {
		return errors.New("unsupported service action")
	}
	snapshot, err := c.Store.Read()
	if err != nil {
		return errors.New("configuration is unavailable or invalid")
	}
	if _, ok := snapshot.Config.Services[id]; !ok {
		return errors.New("managed service does not exist")
	}
	key := "service/" + id
	a := c.Adapters[backend(snapshot.Config, key)]
	life, ok := a.(LifecycleAdapter)
	if !ok {
		return errors.New("service backend does not support lifecycle operations")
	}
	if action != "stop" {
		if err = c.readinessError(ctx, snapshot.Config, id, false); err != nil {
			return err
		}
		needed := map[string]bool{}
		var collect func(string)
		collect = func(id string) {
			if needed["service/"+id] {
				return
			}
			needed["service/"+id] = true
			for _, dep := range snapshot.Config.Services[id].DependsOn {
				collect(dep)
			}
		}
		collect(id)
		for _, issue := range c.Store.CheckFiles(snapshot.Config).Issues {
			if needed[issue.Resource] {
				return errors.New("required service data or private files are unavailable")
			}
		}
		for resource := range needed {
			adapter := c.Adapters[backend(snapshot.Config, resource)]
			if adapter == nil {
				return errors.New("dependency backend is unavailable")
			}
			obs, e := adapter.Inspect(ctx, snapshot.Config, resource)
			if e != nil || !obs.Matches {
				return errors.New("apply the current service and dependency definitions before starting")
			}
		}
	}
	if err = life.Action(ctx, snapshot.Config, key, action); err != nil {
		return fmt.Errorf("service %s failed: %w", action, err)
	}
	return nil
}

// SetEnabled changes boot policy through the same authoritative save/apply path.
// next_start publication must not change the currently running workload.
func (c Coordinator) SetEnabled(ctx context.Context, id string, enabled bool) (Journal, error) {
	if !validID(id) {
		return Journal{}, errors.New("invalid service ID")
	}
	snapshot, err := c.Store.Read()
	if err != nil {
		return Journal{}, errors.New("configuration is unavailable or invalid")
	}
	service, ok := snapshot.Config.Services[id]
	if !ok {
		return Journal{}, errors.New("managed service does not exist")
	}
	if enabled {
		if err = c.readinessError(ctx, snapshot.Config, id, true); err != nil {
			return Journal{}, err
		}
		for _, issue := range c.Store.CheckFiles(snapshot.Config).Issues {
			if issue.Resource == "service/"+id {
				return Journal{}, errors.New("repair service file and mount prerequisites before enabling boot startup")
			}
		}
	}
	service.Enabled = enabled
	snapshot.Config.Services[id] = service
	return c.SaveApply(ctx, snapshot.Config, snapshot.Revision, "next_start")
}
