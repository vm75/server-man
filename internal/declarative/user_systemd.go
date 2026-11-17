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

func userBusIssues(ctx context.Context, program string, runner core.Executor, target string) []Issue {
	if program == "" {
		return nil // The manager probe already reports the missing program.
	}
	issue := Issue{"unknown", "backend/" + target, "systemd", "user_bus_unknown", "The account's D-Bus socket could not be checked.", "Install native user-session D-Bus support if missing, then start the account's dbus.socket or rerun the installer and recheck."}
	cmd := userSystemdCommand(program, "show", "dbus.socket", "--property=LoadState,ActiveState")
	cmd.Timeout = 3 * time.Second
	output, err := runner.Capture(ctx, cmd)
	if err != nil {
		return []Issue{issue}
	}
	fields := map[string]string{}
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			fields[key] = value
		}
	}
	if fields["LoadState"] == "loaded" && fields["ActiveState"] == "active" {
		return nil
	}
	if fields["LoadState"] != "" && fields["ActiveState"] != "" {
		issue.Severity = "error"
		issue.Code = "user_bus_unavailable"
		issue.Message = "The account's D-Bus socket is missing or inactive."
	}
	return []Issue{issue}
}
