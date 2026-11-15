package declarative

import (
	"errors"
	"os"

	"server-man/internal/config"
)

func (c Coordinator) serverRestartRequired(desired Config) bool {
	if c.RunningServer == nil {
		return false
	}
	settings, err := config.Defaults()
	if err != nil {
		return true
	}
	if desired.Server != nil {
		settings = *desired.Server
	}
	running := *c.RunningServer
	settings.Path = ""
	running.Path = ""
	return settings != running
}

func (c Coordinator) checkServerReady() error {
	if c.RunningServer == nil {
		return nil
	}
	snapshot, err := c.Store.Read()
	if os.IsNotExist(err) {
		return nil
	}
	// Invalid resource YAML must remain repairable through the configuration editor.
	if err != nil {
		return nil
	}
	if c.serverRestartRequired(snapshot.Config) {
		return errors.New("server settings changed; restart server-man before further managed operations")
	}
	return nil
}

func (c Coordinator) checkMutationReady() error {
	if err := c.checkBackupIdle(); err != nil {
		return err
	}
	return c.checkServerReady()
}
