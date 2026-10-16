// Package compose manages filesystem-backed Compose projects through installed CLIs.
package compose

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	"unicode/utf8"

	"serverman/internal/core"
)

var projectName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
var dataDirectoryName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)
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

type File struct {
	Content  string `json:"content"`
	Revision string `json:"revision"`
}

type resolvedConfig struct {
	Services map[string]resolvedService `json:"services"`
	Secrets  map[string]any             `json:"secrets"`
	Configs  map[string]any             `json:"configs"`
}

type resolvedService struct {
	Privileged        bool              `json:"privileged"`
	Devices           []json.RawMessage `json:"devices"`
	DeviceCgroupRules []string          `json:"device_cgroup_rules"`
	CapAdd            []string          `json:"cap_add"`
	VolumesFrom       []string          `json:"volumes_from"`
	NetworkMode       string            `json:"network_mode"`
	PID               string            `json:"pid"`
	IPC               string            `json:"ipc"`
	UTS               string            `json:"uts"`
	Cgroup            string            `json:"cgroup"`
	UsernsMode        string            `json:"userns_mode"`
	Runtime           string            `json:"runtime"`
	SecurityOpt       []string          `json:"security_opt"`
	Volumes           []struct {
		Type   string `json:"type"`
		Source string `json:"source"`
	} `json:"volumes"`
	EnvFiles json.RawMessage `json:"env_file"`
	Build    json.RawMessage `json:"build"`
	Provider json.RawMessage `json:"provider"`
}

type Backend struct {
	vmRoot        string
	roots         map[string]string
	programs      map[string]string
	runner        core.Executor
	allowFileRead bool
}

func New(vmRoot string, roots map[string]string, runner core.Executor, allowFileRead ...bool) *Backend {
	b := &Backend{vmRoot: vmRoot, roots: map[string]string{}, programs: map[string]string{}, runner: runner}
	if len(allowFileRead) > 0 {
		b.allowFileRead = allowFileRead[0]
	}
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
	realRoot, rootErr := filepath.EvalSymlinks(root)
	if rootErr != nil || project.realDirectory != filepath.Join(realRoot, name) {
		return project, core.Invalid("project directory must not redirect to another project")
	}
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
	if dataInfo, err := os.Lstat(projectData); err == nil {
		if dataInfo.Mode()&os.ModeSymlink != 0 {
			return project, core.Invalid("project data directory must not be a symlink")
		}
		if err := contained(dataRoot, projectData); err != nil {
			return project, core.Invalid("project data directory escapes the configured data root")
		}
		dataRootReal, rootErr := filepath.EvalSymlinks(dataRoot)
		dataReal, dataErr := filepath.EvalSymlinks(projectData)
		if rootErr != nil || dataErr != nil || dataReal != filepath.Join(dataRootReal, name) {
			return project, core.Invalid("project data directory must not redirect to another project")
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
		ok, err := regularWithin(projectData, path)
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
	return b.commandFile(project, project.File, args...)
}

func (b *Backend) commandFile(project Project, file string, args ...string) (core.Command, error) {
	program := b.programs[project.Engine]
	if program == "" {
		return core.Command{}, core.Unavailable(project.Engine + " executable is unavailable")
	}
	arguments := []string{"compose", "--project-directory", project.Directory, "-f", file}
	for _, file := range project.EnvFiles {
		arguments = append(arguments, "--env-file", file)
	}
	arguments = append(arguments, args...)
	return core.Command{Program: program, Args: arguments, Dir: project.Directory, Env: []string{"PODMAN_COMPOSE_WARNING_LOGS=false", "COMPOSE_ANSI=never", "COMPOSE_PROGRESS=plain"}}, nil
}

func projectedPath(root, source string) (string, error) {
	if !filepath.IsAbs(source) {
		source = filepath.Join(root, source)
	}
	source = filepath.Clean(source)
	current := source
	var suffix []string
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return filepath.Clean(resolved), nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}

func underAny(source string, roots ...string) bool {
	if !filepath.IsAbs(source) {
		return false
	}
	for _, root := range roots {
		realRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			continue
		}
		resolved, err := projectedPath(root, source)
		if err == nil && filepath.IsAbs(filepath.Clean(resolved)) {
			relative, relErr := filepath.Rel(realRoot, resolved)
			if relErr == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative) {
				return true
			}
		}
	}
	return false
}

func resolvedEnvPaths(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var names []string
	if err := json.Unmarshal(raw, &names); err == nil {
		return names, nil
	}
	var entries []struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.Path == "" {
			return nil, errors.New("empty environment file path")
		}
		names = append(names, entry.Path)
	}
	return names, nil
}

func buildContext(raw json.RawMessage, projectDir string) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var contextPath string
	if err := json.Unmarshal(raw, &contextPath); err == nil {
		return contextPath, nil
	}
	var config struct {
		Context            string          `json:"context"`
		AdditionalContexts json.RawMessage `json:"additional_contexts"`
		Secrets            json.RawMessage `json:"secrets"`
		SSH                json.RawMessage `json:"ssh"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		return "", err
	}
	for _, option := range []json.RawMessage{config.AdditionalContexts, config.Secrets, config.SSH} {
		if len(option) > 0 && string(option) != "null" && string(option) != "[]" && string(option) != "{}" {
			return "", errors.New("external build resources are disabled")
		}
	}
	if config.Context == "" {
		return projectDir, nil
	}
	return config.Context, nil
}

func (b *Backend) validateCompose(ctx context.Context, project Project, file string) error {
	command, err := b.commandFile(project, file, "config", "--format", "json")
	if err != nil {
		return err
	}
	command.Timeout = 30 * time.Second
	output, err := b.runner.Capture(ctx, command)
	if err != nil {
		// Provider diagnostics can contain interpolated secrets.
		return core.Invalid("Compose configuration could not be validated")
	}
	var config resolvedConfig
	if err := json.Unmarshal([]byte(output), &config); err != nil || config.Services == nil {
		return core.Invalid("Compose provider did not return valid resolved JSON")
	}
	if len(config.Secrets) != 0 || len(config.Configs) != 0 {
		return core.Invalid("Compose secrets/config mounts are not allowed by the per-project filesystem policy")
	}
	dataRoot := filepath.Join(b.vmRoot, "data", project.Name)
	for _, service := range config.Services {
		if service.Privileged || len(service.Devices) != 0 || len(service.DeviceCgroupRules) != 0 || len(service.CapAdd) != 0 || len(service.VolumesFrom) != 0 || service.Runtime != "" || len(service.Provider) != 0 {
			return core.Invalid("Compose requests privileged host or device access")
		}
		for _, namespace := range []string{service.NetworkMode, service.PID, service.IPC, service.UTS, service.Cgroup, service.UsernsMode} {
			if namespace == "host" {
				return core.Invalid("Compose host namespaces are not allowed by the per-project isolation policy")
			}
		}
		for _, option := range service.SecurityOpt {
			value := strings.ToLower(strings.TrimSpace(option))
			if value != "no-new-privileges:true" && value != "no-new-privileges=true" {
				return core.Invalid("Compose security relaxation is not allowed")
			}
		}
		for _, mount := range service.Volumes {
			switch mount.Type {
			case "bind":
				if mount.Source == "" || !underAny(mount.Source, dataRoot) {
					return core.Invalid("Compose bind mounts must stay inside this project's data directory")
				}
			case "tmpfs":
			case "volume":
				return core.Invalid("named volumes are disabled; store persistent data under this project's data directory")
			default:
				return core.Invalid("Compose mount type is unsupported by the per-project isolation policy")
			}
		}
		envFiles, err := resolvedEnvPaths(service.EnvFiles)
		if err != nil {
			return core.Invalid("Compose service environment file is invalid")
		}
		for _, envFile := range envFiles {
			if !underAny(envFile, dataRoot) {
				return core.Invalid("Compose service environment files must stay inside this project's data directory")
			}
		}
		contextPath, err := buildContext(service.Build, project.Directory)
		if err != nil {
			return core.Invalid("Compose build context is invalid")
		}
		if contextPath != "" && !underAny(contextPath, project.realDirectory, dataRoot) {
			return core.Invalid("Compose build context must stay inside this project's config or data directory")
		}
	}
	return nil
}

func fileRevision(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func isNotFound(err error) bool {
	var problem *core.Error
	return errors.As(err, &problem) && problem.Code == "not_found"
}

func readProjectFile(root, path string, limit int64) (File, error) {
	exists, err := regularWithin(root, path)
	if err != nil {
		return File{}, core.Invalid("configuration file is inaccessible or escapes its project directory")
	}
	if !exists {
		return File{}, &core.Error{Code: "not_found", Message: "configuration file not found", Status: 404}
	}
	file, err := os.Open(path)
	if err != nil {
		return File{}, core.Invalid("configuration file could not be opened")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit || !utf8.Valid(data) {
		return File{}, core.Invalid("configuration file exceeds the editor size limit")
	}
	return File{Content: string(data), Revision: fileRevision(data)}, nil
}

func atomicPrivateFile(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".serverman-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func (b *Backend) composePath(engine, name string) (string, error) {
	root, ok := b.roots[engine]
	if !ok || !projectName.MatchString(name) {
		return "", core.Invalid("invalid Compose project")
	}
	return filepath.Join(root, name, "compose.yaml"), nil
}

func envFilename(name string) bool {
	base := strings.TrimSuffix(name, ".env")
	return name != "data_root.env" && name != "global.env" && len(name) >= 5 && len(name) <= 128 && projectName.MatchString(base) && strings.HasSuffix(name, ".env")
}

func (b *Backend) EnvFile(engine, name, filename string) (File, error) {
	if _, err := b.resolve(engine, name); err != nil {
		return File{}, err
	}
	if !envFilename(filename) {
		return File{}, core.Invalid("environment filename must be a simple name ending in .env")
	}
	projectData := filepath.Join(b.vmRoot, "data", name)
	return readProjectFile(projectData, filepath.Join(projectData, filename), 64<<10)
}

func (b *Backend) ComposeFile(engine, name string) (File, error) {
	project, err := b.resolve(engine, name)
	if err != nil {
		return File{}, err
	}
	return readProjectFile(project.realDirectory, project.realFile, 256<<10)
}

func ensureDirectory(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return core.Invalid("project directory could not be created")
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return core.Invalid("project directory is inaccessible")
	}
	return nil
}

func ensureContainedDirectory(root, path string) (bool, error) {
	if err := ensureDirectory(root); err != nil {
		return false, err
	}
	if info, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(path, 0700); err != nil {
			return false, core.Invalid("project data directory could not be created")
		}
		if err := contained(root, path); err != nil {
			_ = os.Remove(path)
			return false, core.Invalid("project data directory escapes the configured root")
		}
		return true, nil
	} else if err != nil {
		return false, core.Invalid("project data directory could not be checked")
	} else if info.Mode()&os.ModeSymlink != 0 {
		return false, core.Invalid("project data directory must not be a symlink")
	}
	if err := contained(root, path); err != nil {
		return false, core.Invalid("project data directory escapes the configured root")
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return false, core.Invalid("project data path is not a directory")
	}
	return false, nil
}

func createContainedPath(root, relative string) error {
	current := root
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		if info, err := os.Lstat(current); err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return core.Invalid("data path contains a symlink or non-directory")
			}
		} else if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(current, 0700); err != nil {
				return core.Invalid("data directory could not be created")
			}
		} else {
			return core.Invalid("data directory could not be checked")
		}
		if err := contained(root, current); err != nil {
			return core.Invalid("data directory escapes its project")
		}
	}
	return nil
}

func (b *Backend) CreateProject(ctx context.Context, engine, name, content string, output io.Writer) (any, error) {
	if err := validateText(content, 256<<10); err != nil {
		return nil, err
	}
	path, err := b.composePath(engine, name)
	if err != nil {
		return nil, err
	}
	root := b.roots[engine]
	if err := ensureDirectory(root); err != nil {
		return nil, err
	}
	directory := filepath.Dir(path)
	if err := os.Mkdir(directory, 0700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, &core.Error{Code: "already_exists", Message: "project already exists", Status: 409}
		}
		return nil, core.Invalid("project directory could not be created")
	}
	createdData, err := ensureContainedDirectory(filepath.Join(b.vmRoot, "data"), filepath.Join(b.vmRoot, "data", name))
	if err != nil {
		_ = os.Remove(directory)
		return nil, err
	}
	cleanup := func() {
		_ = os.Remove(path)
		_ = os.Remove(directory)
		if createdData {
			_ = os.Remove(filepath.Join(b.vmRoot, "data", name))
		}
	}
	if err := contained(root, directory); err != nil {
		cleanup()
		return nil, core.Invalid("project directory escapes its configured root")
	}
	if err := atomicPrivateFile(path, []byte(content)); err != nil {
		cleanup()
		return nil, core.Invalid("Compose file could not be written")
	}
	project, err := b.resolve(engine, name)
	if err == nil {
		err = b.validateCompose(ctx, project, path)
	}
	if err != nil {
		cleanup()
		return nil, err
	}
	return map[string]string{"engine": engine, "project": name, "file": path, "status": "created"}, nil
}

func (b *Backend) SaveCompose(ctx context.Context, engine, name, revision, content string, output io.Writer) (any, error) {
	project, err := b.resolve(engine, name)
	if err != nil {
		return nil, err
	}
	current, err := readProjectFile(project.realDirectory, project.realFile, 256<<10)
	if err != nil {
		return nil, err
	}
	if current.Revision != revision {
		return nil, &core.Error{Code: "stale_revision", Message: "Compose file changed; reload it before saving", Status: 409}
	}
	if err := validateText(content, 256<<10); err != nil {
		return nil, err
	}
	candidate, err := os.CreateTemp(project.realDirectory, ".serverman-compose-*.yaml")
	if err != nil {
		return nil, core.Invalid("Compose file could not be prepared")
	}
	temporary := candidate.Name()
	defer os.Remove(temporary)
	if err := candidate.Chmod(0600); err != nil {
		_ = candidate.Close()
		return nil, core.Invalid("Compose file could not be prepared")
	}
	if _, err := candidate.WriteString(content); err != nil {
		_ = candidate.Close()
		return nil, core.Invalid("Compose file could not be prepared")
	}
	if err := candidate.Close(); err != nil {
		return nil, core.Invalid("Compose file could not be prepared")
	}
	if err := b.validateCompose(ctx, project, temporary); err != nil {
		return nil, err
	}
	latest, err := readProjectFile(project.realDirectory, project.realFile, 256<<10)
	if err != nil || latest.Revision != revision {
		return nil, &core.Error{Code: "stale_revision", Message: "Compose file changed while saving; reload it before retrying", Status: 409}
	}
	if err := atomicPrivateFile(project.realFile, []byte(content)); err != nil {
		return nil, core.Invalid("Compose file could not be saved")
	}
	return map[string]string{"engine": engine, "project": name, "status": "saved"}, nil
}

func validateText(content string, limit int) error {
	if len(content) == 0 || len(content) > limit || !utf8.ValidString(content) || strings.ContainsRune(content, '\x00') {
		return core.Invalid("file content is empty, oversized, or invalid text")
	}
	return nil
}

func validateEnvText(content string) error {
	if len(content) > 64<<10 || !utf8.ValidString(content) || strings.ContainsRune(content, '\x00') {
		return core.Invalid("environment content is oversized or invalid text")
	}
	return nil
}

func (b *Backend) SaveEnv(ctx context.Context, engine, name, filename, revision, content string) (any, error) {
	_, err := b.resolve(engine, name)
	if err != nil {
		return nil, err
	}
	if !envFilename(filename) {
		return nil, core.Invalid("environment filename must be a simple name ending in .env")
	}
	if err := validateEnvText(content); err != nil {
		return nil, err
	}
	dataRoot := filepath.Join(b.vmRoot, "data")
	directory := filepath.Join(dataRoot, name)
	if _, err := ensureContainedDirectory(dataRoot, directory); err != nil {
		return nil, err
	}
	path := filepath.Join(directory, filename)
	current, err := readProjectFile(directory, path, 64<<10)
	var previous []byte
	previousExists := false
	if err != nil {
		var problem *core.Error
		if !errors.As(err, &problem) || problem.Code != "not_found" || revision != "missing" {
			return nil, err
		}
	} else if revision == "missing" || current.Revision != revision {
		return nil, &core.Error{Code: "stale_revision", Message: "Environment file changed; reload it before saving", Status: 409}
	} else {
		previous = []byte(current.Content)
		previousExists = true
	}
	latest, latestErr := readProjectFile(directory, path, 64<<10)
	if revision == "missing" {
		if latestErr == nil || !isNotFound(latestErr) {
			return nil, &core.Error{Code: "stale_revision", Message: "Environment file changed; reload it before saving", Status: 409}
		}
	} else if latestErr != nil || latest.Revision != revision {
		return nil, &core.Error{Code: "stale_revision", Message: "Environment file changed; reload it before saving", Status: 409}
	}
	if err := atomicPrivateFile(path, []byte(content)); err != nil {
		return nil, core.Invalid("environment file could not be saved")
	}
	validatedProject, resolveErr := b.resolve(engine, name)
	if resolveErr != nil {
		if previousExists {
			_ = atomicPrivateFile(path, previous)
		} else {
			_ = os.Remove(path)
		}
		return nil, resolveErr
	}
	if err := b.validateCompose(ctx, validatedProject, validatedProject.realFile); err != nil {
		if previousExists {
			_ = atomicPrivateFile(path, previous)
		} else {
			_ = os.Remove(path)
		}
		return nil, err
	}
	return map[string]string{"engine": engine, "project": name, "filename": filename, "status": "saved"}, nil
}

func (b *Backend) CreateDataDirectory(engine, name, relative string) (any, error) {
	if _, err := b.resolve(engine, name); err != nil {
		return nil, err
	}
	if relative == "" || filepath.IsAbs(relative) || filepath.Clean(relative) != relative || strings.ContainsAny(relative, "\\%") {
		return nil, core.Invalid("data directory must be a relative path under the project")
	}
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		if part == "." || part == ".." || !dataDirectoryName.MatchString(part) {
			return nil, core.Invalid("data directory contains an unsupported path component")
		}
	}
	dataRoot := filepath.Join(b.vmRoot, "data")
	projectData := filepath.Join(dataRoot, name)
	if _, err := ensureContainedDirectory(dataRoot, projectData); err != nil {
		return nil, err
	}
	if err := createContainedPath(projectData, relative); err != nil {
		return nil, err
	}
	directory := filepath.Join(projectData, relative)
	return map[string]string{"engine": engine, "project": name, "directory": directory, "status": "created"}, nil
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
	if slices.Contains([]string{"up", "start", "restart"}, action) {
		if err := b.validateCompose(ctx, project, project.realFile); err != nil {
			return nil, err
		}
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
