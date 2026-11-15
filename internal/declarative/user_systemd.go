package declarative

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"server-man/internal/core"
)

func userSystemdCommand(program string, args ...string) core.Command {
	return core.Command{Program: program, Args: append([]string{"--user", "--no-pager", "--no-ask-password"}, args...), Timeout: 15 * time.Second}
}
func userManagerIssues(ctx context.Context, program string, runner core.Executor, target string) []Issue {
	issues := []Issue{}
	add := func(severity, field, code, message, remedy string) {
		issues = append(issues, Issue{severity, "backend/" + target, field, code, message, remedy})
	}
	if program == "" {
		add("error", "systemctl", "missing_dependency", "systemctl is missing.", "Install systemd user-management tools and recheck.")
	}
	if program != "" {
		cmd := userSystemdCommand(program, "show", "--property=Version", "--value")
		cmd.Timeout = 3 * time.Second
		if _, err := runner.Capture(ctx, cmd); err != nil {
			add("error", "systemd", "user_manager_unavailable", "The account's systemd user manager is unavailable.", "Start/configure the account's user manager and runtime bus, then recheck.")
		}
	}
	if loginctl, err := exec.LookPath("loginctl"); err == nil {
		result, err := runner.Capture(ctx, core.Command{Program: loginctl, Args: []string{"show-user", fmt.Sprint(os.Getuid()), "--property=Linger", "--value"}, Timeout: 3 * time.Second})
		if err != nil {
			add("unknown", "autostart", "linger_unknown", "Automatic startup after logout could not be verified.", "Verify lingering for the service account before relying on boot startup.")
		} else if strings.TrimSpace(result) != "yes" {
			add("error", "autostart", "linger_disabled", "User services may not start at boot or survive logout.", "Enable lingering explicitly for the service account in host setup.")
		}
	} else {
		add("unknown", "autostart", "linger_unknown", "Automatic user-manager startup could not be verified.", "Install loginctl or verify user-manager boot setup explicitly.")
	}
	return issues
}
