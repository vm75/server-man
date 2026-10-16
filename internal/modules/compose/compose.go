// Package compose manages filesystem-backed Compose projects through installed CLIs.
package compose

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"serverman/internal/core"
)

var projectName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
var composeFiles = []string{"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml"}

type Engine struct {
	Name      string `json:"name"`
	Root      string `json:"root"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
	Notice    string `json:"notice,omitempty"`
}

type Project struct {
	Engine        string   `json:"engine"`
	Name          string   `json:"name"`
	Directory     string   `json:"directory"`
	File          string   `json:"file"`
	EnvFiles      []string `json:"envFiles"`
	Disabled      bool     `json:"disabled"`
	State         string   `json:"state"`
	Containers    int      `json:"containers"`
	Running       int      `json:"running"`
	realDirectory string
	realFile      string
}

type Inventory struct {
	Engines  []Engine  `json:"engines"`
	Projects []Project `json:"projects"`
	Warnings []string  `json:"warnings"`
}
type Details struct {
	Project    Project     `json:"project"`
	Containers []Container `json:"containers"`
	Pods       []Pod       `json:"pods"`
	Warnings   []string    `json:"warnings"`
}
type Logs struct {
	Text      string `json:"text"`
	Truncated bool   `json:"truncated"`
}

type Backend struct {
	vmRoot   string
	roots    map[string]string
	programs map[string]string
	runner   core.Executor
}

func New(vmRoot string, roots map[string]string, runner core.Executor) *Backend {
	b := &Backend{vmRoot: vmRoot, roots: map[string]string{}, programs: map[string]string{}, runner: runner}
	for _, name := range []string{"podman", "docker"} {
		root := roots[name]
		if root == "" {
			root = filepath.Join(vmRoot, "config", name)
		}
		b.roots[name] = root
		if program, err := exec.LookPath(name); err == nil {
			b.programs[name] = program
		}
	}
	return b
}

// Discover reads names and file metadata only; it never opens Compose/env contents.
func (b *Backend) Discover(engine string) (Inventory, error) {
	result := Inventory{Engines: []Engine{}, Projects: []Project{}, Warnings: []string{}}
	if engine != "" && engine != "podman" && engine != "docker" {
		return result, core.Invalid("unknown container engine")
	}
	for _, name := range []string{"podman", "docker"} {
		if engine != "" && engine != name {
			continue
		}
		status := Engine{Name: name, Root: b.roots[name], Available: b.programs[name] != ""}
		if !status.Available {
			status.Reason = name + " executable was not found; install it and restart server-man"
		}
		entries, err := os.ReadDir(status.Root)
		if errors.Is(err, os.ErrNotExist) {
			status.Notice = "Compose directory does not exist yet"
		} else if err != nil {
			status.Notice = "Compose directory could not be read"
		}
		for _, entry := range entries {
			if len(result.Projects) >= 1024 {
				result.Warnings = append(result.Warnings, "Project discovery is limited to 1024 projects")
				break
			}
			if !entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
				continue
			}
			if !projectName.MatchString(entry.Name()) {
				result.Warnings = append(result.Warnings, name+": skipped a directory with an unsupported project name")
				continue
			}
			project, err := b.resolve(name, entry.Name())
			if err != nil {
				var problem *core.Error
				if !errors.As(err, &problem) || problem.Code != "not_found" {
					result.Warnings = append(result.Warnings, name+"/"+entry.Name()+": "+err.Error())
				}
				continue
			}
			result.Projects = append(result.Projects, project)
		}
		result.Engines = append(result.Engines, status)
	}
	slices.SortFunc(result.Projects, func(a, c Project) int {
		if a.Engine != c.Engine {
			return strings.Compare(a.Engine, c.Engine)
		}
		return strings.Compare(a.Name, c.Name)
	})
	return result, nil
}

func contained(root, path string) error {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(realRoot, realPath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return core.Invalid("path escapes its configured directory")
	}
	return nil
}

func regularWithin(root, path string) (bool, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := contained(root, path); err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, core.Invalid("expected a regular configuration file")
	}
	return true, nil
}

func (b *Backend) resolve(engine, name string) (Project, error) {
	project := Project{Engine: engine, Name: name, EnvFiles: []string{}, State: "unknown"}
	root, ok := b.roots[engine]
	if !ok {
		return project, core.Invalid("unknown container engine")
	}
	if !projectName.MatchString(name) {
		return project, core.Invalid("invalid project name")
	}
	project.Directory = filepath.Join(root, name)
	if err := contained(root, project.Directory); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return project, &core.Error{Code: "not_found", Message: "project directory not found", Status: 404}
		}
		return project, core.Invalid("project directory is inaccessible or escapes its configured root")
	}
	project.realDirectory, _ = filepath.EvalSymlinks(project.Directory)
	for _, file := range composeFiles {
		path := filepath.Join(project.Directory, file)
		exists, err := regularWithin(project.Directory, path)
		if err != nil {
			return project, core.Invalid("Compose file is inaccessible or escapes the project directory")
		}
		if exists {
			project.File = path
			break
		}
	}
	if project.File == "" {
		return project, &core.Error{Code: "not_found", Message: "Compose file not found", Status: 404}
	}
	project.realFile, _ = filepath.EvalSymlinks(project.File)
	dataRoot := filepath.Join(b.vmRoot, "data")
	for _, name := range []string{"data_root.env", "global.env"} {
		path := filepath.Join(dataRoot, name)
		exists, err := regularWithin(dataRoot, path)
		if err != nil {
			return project, core.Invalid("global environment file is inaccessible or escapes the data directory")
		}
		if exists {
			project.EnvFiles = append(project.EnvFiles, path)
		}
	}
	projectData := filepath.Join(dataRoot, name)
	if _, err := os.Stat(projectData); err == nil {
		if err := contained(dataRoot, projectData); err != nil {
			return project, core.Invalid("project data directory escapes the configured data root")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return project, core.Invalid("project data directory could not be read")
	}
	if _, err := os.Lstat(filepath.Join(projectData, ".disabled")); err == nil {
		project.Disabled = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return project, core.Invalid("project disabled marker could not be checked")
	}
	files, err := filepath.Glob(filepath.Join(projectData, "*.env"))
	if err != nil {
		return project, err
	}
	for _, path := range files {
		if strings.HasPrefix(filepath.Base(path), ".") {
			continue
		}
		ok, err := regularWithin(dataRoot, path)
		if err != nil {
			return project, core.Invalid("project environment file is inaccessible or escapes the data directory")
		}
		if ok {
			project.EnvFiles = append(project.EnvFiles, path)
		}
		if len(project.EnvFiles) > 128 {
			return project, core.Invalid("project has more than 128 environment files")
		}
	}
	return project, nil
}

func (b *Backend) command(project Project, args ...string) (core.Command, error) {
	program := b.programs[project.Engine]
	if program == "" {
		return core.Command{}, core.Unavailable(project.Engine + " executable is unavailable")
	}
	arguments := []string{"compose", "--project-directory", project.Directory, "-f", project.File}
	for _, file := range project.EnvFiles {
		arguments = append(arguments, "--env-file", file)
	}
	arguments = append(arguments, args...)
	return core.Command{Program: program, Args: arguments, Dir: project.Directory, Env: []string{"PODMAN_COMPOSE_WARNING_LOGS=false", "COMPOSE_ANSI=never", "COMPOSE_PROGRESS=plain"}}, nil
}

func (b *Backend) Inventory(ctx context.Context, engine string) (Inventory, error) {
	result, err := b.Discover(engine)
	if err != nil {
		return result, err
	}
	for i := range result.Engines {
		backend := &result.Engines[i]
		hasProjects := slices.ContainsFunc(result.Projects, func(p Project) bool { return p.Engine == backend.Name })
		if !backend.Available || !hasProjects {
			continue
		}
		command := core.Command{Program: b.programs[backend.Name], Args: []string{"ps", "--all", "--format", "json"}, Timeout: 5 * time.Second}
		output, err := b.runner.Capture(ctx, command)
		if err != nil {
			backend.Notice = "Runtime inventory is unavailable; check engine access and permissions"
			continue
		}
		containers, err := parseContainers(output)
		if err != nil {
			backend.Notice = "Runtime returned unsupported container JSON"
			continue
		}
		for j := range result.Projects {
			project := &result.Projects[j]
			if project.Engine != backend.Name {
				continue
			}
			project.State = "not_detected"
			matched := []Container{}
			for _, container := range containers {
				if matches(container, *project) {
					matched = append(matched, container)
				}
			}
			if len(matched) > 0 {
				project.Containers, project.Running, project.State = summarize(matched)
			}
		}
	}
	return result, nil
}

func (b *Backend) Status(ctx context.Context, engine, name string) (Details, error) {
	detail := Details{Containers: []Container{}, Pods: []Pod{}, Warnings: []string{}}
	project, err := b.resolve(engine, name)
	if err != nil {
		return detail, err
	}
	detail.Project = project
	command, err := b.command(project, "ps", "--all", "--format", "json")
	if err != nil {
		return detail, err
	}
	command.Timeout = 7 * time.Second
	output, err := b.runner.Capture(ctx, command)
	if err != nil && engine == "podman" && unsupportedAll(err) {
		command, _ = b.command(project, "ps", "--format", "json")
		command.Timeout = 7 * time.Second
		output, err = b.runner.Capture(ctx, command)
	}
	if err != nil {
		return detail, core.Unavailable("Compose status failed; check the engine, provider, project and environment files")
	}
	detail.Containers, err = parseContainers(output)
	if err != nil {
		return detail, core.Unavailable("Compose provider must support JSON container status")
	}
	detail.Project.Containers, detail.Project.Running, detail.Project.State = summarize(detail.Containers)
	if engine == "podman" && slices.ContainsFunc(detail.Containers, func(c Container) bool { return c.PodID != "" }) {
		output, err := b.runner.Capture(ctx, core.Command{Program: b.programs[engine], Args: []string{"pod", "ps", "--format", "json"}, Timeout: 5 * time.Second})
		if err == nil {
			detail.Pods, err = parsePods(output, detail.Containers)
		}
		if err != nil {
			detail.Warnings = append(detail.Warnings, "Pod details could not be read; container pod IDs are still shown")
		}
	}
	return detail, nil
}

func unsupportedAll(err error) bool {
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "--all") && (strings.Contains(text, "unrecognized") || strings.Contains(text, "unknown flag") || strings.Contains(text, "no such option"))
}

func (b *Backend) Action(ctx context.Context, engine, name, action string, output io.Writer) (any, error) {
	if !slices.Contains([]string{"up", "start", "stop", "restart", "pull", "down"}, action) {
		return nil, core.Invalid("unsupported Compose action")
	}
	project, err := b.resolve(engine, name)
	if err != nil {
		return nil, err
	}
	if project.Disabled && action != "stop" && action != "down" {
		return nil, &core.Error{Code: "project_disabled", Message: "Project is disabled by its data/" + name + "/.disabled marker. Remove that marker on the host before starting, restarting or pulling.", Status: 409}
	}
	args := []string{action}
	if action == "up" {
		args = append(args, "-d")
	}
	command, err := b.command(project, args...)
	if err != nil {
		return nil, err
	}
	if err := b.runner.Run(ctx, command, output); err != nil {
		return nil, fmt.Errorf("Compose %s failed: %w", action, err)
	}
	return map[string]string{"engine": engine, "project": name, "action": action}, nil
}

func (b *Backend) Logs(ctx context.Context, engine, name, service string, tail int) (Logs, error) {
	if service != "" && !projectName.MatchString(service) {
		return Logs{}, core.Invalid("invalid service name")
	}
	if tail == 0 {
		tail = 200
	}
	if tail < 1 || tail > 2000 {
		return Logs{}, core.Invalid("tail must be between 1 and 2000")
	}
	project, err := b.resolve(engine, name)
	if err != nil {
		return Logs{}, err
	}
	args := []string{"logs", "--no-color", "--tail", strconv.Itoa(tail)}
	if service != "" {
		args = append(args, service)
	}
	command, err := b.command(project, args...)
	if err != nil {
		return Logs{}, err
	}
	command.Timeout = 15 * time.Second
	output := core.NewTail(128 << 10)
	if err := b.runner.Run(ctx, command, output); err != nil {
		return Logs{}, core.Unavailable("Compose logs could not be read; check the provider and engine permissions")
	}
	text, truncated := output.Snapshot()
	return Logs{Text: text, Truncated: truncated}, nil
}
