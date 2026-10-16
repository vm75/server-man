package shares

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"serverman/internal/core"
)

type export struct {
	path, client string
	options      []string
}

func (e export) key() string { return e.path + "\x00" + e.client }

var safeOptions = regexp.MustCompile(`^[A-Za-z0-9_,=:.+/-]+$`)

func exportFields(line string) ([]string, error) {
	fields := []string{}
	var current strings.Builder
	quoted := false
	flush := func() {
		if current.Len() > 0 {
			fields = append(fields, current.String())
			current.Reset()
		}
	}
	for i := 0; i < len(line); i++ {
		c := line[i]
		if c == '"' {
			quoted = !quoted
			continue
		}
		if c == '#' && !quoted {
			break
		}
		if c == '\\' && i+3 < len(line) {
			if n, err := strconv.ParseUint(line[i+1:i+4], 8, 8); err == nil {
				current.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		if (c == ' ' || c == '\t' || c == '\r') && !quoted {
			flush()
		} else {
			current.WriteByte(c)
		}
	}
	if quoted {
		return nil, errors.New("unterminated export path quote")
	}
	flush()
	return fields, nil
}
func normalizeSelector(value string) string {
	if result, err := client(value); err == nil {
		return result
	}
	return value
}
func parseExports(text string) ([]export, error) {
	result := []export{}
	text = strings.ReplaceAll(text, "\\\n", "")
	for _, line := range strings.Split(text, "\n") {
		fields, err := exportFields(line)
		if err != nil {
			return nil, err
		}
		if len(fields) == 0 {
			continue
		}
		if !filepath.IsAbs(fields[0]) || len(fields) < 2 {
			return nil, errors.New("unsupported NFS export line")
		}
		defaults := []string{}
		for _, field := range fields[1:] {
			if strings.HasPrefix(field, "-") {
				defaults = strings.Split(strings.TrimPrefix(field, "-"), ",")
				continue
			}
			selector, opts := field, append([]string{}, defaults...)
			if at := strings.IndexByte(field, '('); at >= 0 {
				if !strings.HasSuffix(field, ")") {
					return nil, errors.New("unsupported NFS export options")
				}
				selector = field[:at]
				opts = append(opts, strings.Split(field[at+1:len(field)-1], ",")...)
			}
			if selector == "" {
				return nil, errors.New("missing NFS client selector")
			}
			result = append(result, export{path: fields[0], client: normalizeSelector(selector), options: opts})
			if len(result) > 4096 {
				return nil, errors.New("NFS inventory exceeds 4096 entries")
			}
		}
	}
	return result, nil
}
func (b *Backend) nfsConfigured() ([]export, error) {
	files := []string{b.nfsExports}
	entries, err := os.ReadDir(b.nfsDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".") && strings.HasSuffix(entry.Name(), ".exports") {
			files = append(files, filepath.Join(b.nfsDir, entry.Name()))
		}
	}
	if len(files) > 65 {
		return nil, errors.New("NFS configuration exceeds 64 fragments")
	}
	result := []export{}
	total := 0
	for _, file := range files {
		if file == b.nfsManaged {
			continue
		}
		data, err := readFile(file, false)
		if err != nil {
			return nil, err
		}
		total += len(data.data)
		if total > maxFile {
			return nil, errors.New("NFS configuration exceeds 1 MiB")
		}
		items, err := parseExports(string(data.data))
		if err != nil {
			return nil, err
		}
		result = append(result, items...)
		if len(result) > 4096 {
			return nil, errors.New("NFS configured inventory exceeds 4096 entries")
		}
	}
	return result, nil
}
func (b *Backend) nfsActive(ctx context.Context) ([]export, error) {
	if b.programs["exportfs"] == "" {
		return nil, errors.New("exportfs is unavailable")
	}
	text, err := b.runner.Capture(ctx, core.Command{Program: b.programs["exportfs"], Args: []string{"-s"}, Timeout: 5 * time.Second})
	if err != nil {
		return nil, err
	}
	return parseExports(text)
}
func pairs(defs []Definition) map[string]export {
	result := map[string]export{}
	for _, d := range defs {
		for _, selector := range d.Clients {
			e := export{path: d.Path, client: selector, options: strings.Split(options(d), ",")}
			result[e.key()] = e
		}
	}
	return result
}
func exportMap(items []export) map[string]export {
	result := map[string]export{}
	for _, e := range items {
		result[e.key()] = e
	}
	return result
}
func optionValue(opts []string, key, defaultValue string) string {
	value := defaultValue
	for _, opt := range opts {
		if strings.HasPrefix(opt, key+"=") {
			value = strings.TrimPrefix(opt, key+"=")
		}
	}
	return value
}
func flag(opts []string, yes, no string, defaultValue bool) bool {
	value := defaultValue
	for _, opt := range opts {
		if opt == yes {
			value = true
		}
		if opt == no {
			value = false
		}
	}
	return value
}
func sameExport(actual, want export) bool {
	return flag(actual.options, "ro", "rw", true) == flag(want.options, "ro", "rw", true) &&
		flag(actual.options, "root_squash", "no_root_squash", true) == flag(want.options, "root_squash", "no_root_squash", true) &&
		flag(actual.options, "sync", "async", true) == flag(want.options, "sync", "async", true) &&
		flag(actual.options, "all_squash", "no_all_squash", false) == flag(want.options, "all_squash", "no_all_squash", false) &&
		flag(actual.options, "secure", "insecure", true) == flag(want.options, "secure", "insecure", true) &&
		flag(actual.options, "subtree_check", "no_subtree_check", true) == flag(want.options, "subtree_check", "no_subtree_check", true) &&
		optionValue(actual.options, "sec", "sys") == optionValue(want.options, "sec", "sys")
}
func (b *Backend) export(ctx context.Context, e export, remove bool, out io.Writer) error {
	args := []string{"-u", target(e.client, e.path)}
	if !remove {
		opts := strings.Join(e.options, ",")
		if !safeOptions.MatchString(opts) {
			return errors.New("unsupported runtime options prevent safe restoration")
		}
		args = []string{"-i", "-o", opts, target(e.client, e.path)}
	}
	return b.runner.Run(ctx, core.Command{Program: b.programs["exportfs"], Args: args, Timeout: 10 * time.Second}, out)
}
func ordered(m map[string]export) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
func (b *Backend) applyNFS(ctx context.Context, before, after []Definition, active []export, out io.Writer) error {
	old, next, current := pairs(before), pairs(after), exportMap(active)
	for _, key := range ordered(next) {
		if err := b.export(ctx, next[key], false, out); err != nil {
			return errors.New("NFS export apply failed")
		}
	}
	for _, key := range ordered(old) {
		if _, keep := next[key]; !keep {
			if _, exists := current[key]; exists {
				if err := b.export(ctx, old[key], true, out); err != nil {
					return errors.New("NFS export removal failed")
				}
			}
		}
	}
	actual, err := b.nfsActive(ctx)
	if err != nil {
		return errors.New("NFS runtime verification failed")
	}
	current = exportMap(actual)
	for key, want := range next {
		got, ok := current[key]
		if !ok || !sameExport(got, want) {
			return errors.New("NFS runtime export table does not match the requested access")
		}
	}
	for key := range old {
		if _, keep := next[key]; !keep {
			if _, exists := current[key]; exists {
				return errors.New("Removed NFS client remains registered")
			}
		}
	}
	return nil
}
func (b *Backend) restoreNFS(ctx context.Context, before, after []Definition, active []export, out io.Writer) error {
	initial := exportMap(active)
	currentList, err := b.nfsActive(ctx)
	if err != nil {
		return err
	}
	current := exportMap(currentList)
	touched := pairs(before)
	for key, e := range pairs(after) {
		touched[key] = e
	}
	var failures []error
	for _, key := range ordered(touched) {
		if old, exists := initial[key]; exists {
			if err := b.export(ctx, old, false, out); err != nil {
				failures = append(failures, err)
			}
		} else if now, exists := current[key]; exists {
			if err := b.export(ctx, now, true, out); err != nil {
				failures = append(failures, err)
			}
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("NFS runtime rollback failed for %d entries", len(failures))
	}
	restored, err := b.nfsActive(ctx)
	if err != nil {
		return err
	}
	restoredMap := exportMap(restored)
	for key := range touched {
		old, had := initial[key]
		now, has := restoredMap[key]
		if had != has || (had && strings.Join(old.options, ",") != strings.Join(now.options, ",")) {
			return errors.New("NFS runtime rollback verification failed")
		}
	}
	return nil
}
