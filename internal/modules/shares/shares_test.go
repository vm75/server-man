package shares

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"server-man/internal/config"
	"server-man/internal/core"
)

type recorder struct {
	mu              sync.Mutex
	commands        []core.Command
	runtime         map[string]export
	captureOverride func(core.Command) (string, error)
	beforeRun       func(context.Context, core.Command) error
	afterRun        func(context.Context, core.Command) error
}

func (r *recorder) record(c core.Command) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commands = append(r.commands, c)
}
func (r *recorder) recorded() []core.Command {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]core.Command{}, r.commands...)
}
func (r *recorder) active() []export {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := []export{}
	for _, key := range ordered(r.runtime) {
		result = append(result, r.runtime[key])
	}
	return result
}
func expandSamba(file string) (string, error) {
	data, err := os.ReadFile(file)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var out strings.Builder
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(strings.ToLower(key)) == "include" {
			included, err := expandSamba(strings.TrimSpace(value))
			if err != nil {
				return "", err
			}
			out.WriteString(included)
		} else {
			out.WriteString(line)
			out.WriteByte('\n')
		}
	}
	return out.String(), nil
}
func (r *recorder) Capture(ctx context.Context, c core.Command) (string, error) {
	r.record(c)
	if r.captureOverride != nil {
		return r.captureOverride(c)
	}
	switch c.Program {
	case "testparm":
		return expandSamba(c.Args[len(c.Args)-1])
	case "smbstatus":
		return `{"tcons":{"1":{"service":"demo","machine":"PRIVATE"}},"sessions":{"SECRET":"PRIVATE"},"open_files":{"PRIVATE":{}}}`, nil
	case "exportfs":
		var text strings.Builder
		for _, e := range r.active() {
			fmt.Fprintf(&text, "%s %s(%s)\n", strings.ReplaceAll(e.path, " ", `\040`), e.client, strings.Join(e.options, ","))
		}
		return text.String(), nil
	}
	return "", errors.New("unexpected capture")
}
func (r *recorder) Run(ctx context.Context, c core.Command, out io.Writer) error {
	r.record(c)
	if r.beforeRun != nil {
		if err := r.beforeRun(ctx, c); err != nil {
			return err
		}
	}
	if c.Program == "exportfs" {
		arg := c.Args[len(c.Args)-1]
		at := strings.LastIndex(arg, ":/")
		if at < 0 {
			return errors.New("bad export target")
		}
		selector := strings.ReplaceAll(strings.ReplaceAll(arg[:at], "[", ""), "]", "")
		e := export{path: arg[at+1:], client: normalizeSelector(selector)}
		r.mu.Lock()
		if c.Args[0] == "-u" {
			delete(r.runtime, e.key())
		} else {
			if len(c.Args) != 4 || c.Args[0] != "-i" || c.Args[1] != "-o" {
				r.mu.Unlock()
				return errors.New("non-targeted export")
			}
			e.options = strings.Split(c.Args[2], ",")
			r.runtime[e.key()] = e
		}
		r.mu.Unlock()
	}
	if r.afterRun != nil {
		if err := r.afterRun(ctx, c); err != nil {
			return err
		}
	}
	_, err := io.WriteString(out, "fixture apply\n")
	return err
}
func put(t *testing.T, file, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
}
func fixture(t *testing.T) (*Backend, *recorder, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "host config")
	cfg := config.Config{SambaConfig: filepath.Join(root, "samba", "smb.conf"), SambaManagedFile: filepath.Join(root, "samba", "server-man.conf")}
	r := &recorder{runtime: map[string]export{}}
	b := New(cfg, r)
	b.nfsExports = filepath.Join(root, "exports")
	b.nfsDir = filepath.Join(root, "exports.d")
	b.nfsManaged = filepath.Join(b.nfsDir, "server-man.exports")
	for _, program := range []string{"testparm", "smbcontrol", "smbstatus", "exportfs"} {
		b.programs[program] = program
	}
	data := filepath.Join(root, "shared data")
	if err := os.MkdirAll(data, 0755); err != nil {
		t.Fatal(err)
	}
	put(t, b.sambaConfig, "[global]\n\tworkgroup = WORKGROUP\n[legacy]\n\tpath = /legacy\n\tinclude = "+b.sambaManaged+"\n")
	put(t, b.nfsExports, "/legacy 10.9.0.0/24(ro,sync,root_squash,no_subtree_check,sec=sys)\n")
	if err := os.MkdirAll(b.nfsDir, 0755); err != nil {
		t.Fatal(err)
	}
	legacy := export{path: "/legacy", client: "10.9.0.0/24", options: []string{"ro", "sync", "root_squash", "no_subtree_check", "sec=sys"}}
	r.runtime[legacy.key()] = legacy
	return b, r, data
}
func revision(t *testing.T, b *Backend, p string) string {
	t.Helper()
	s, err := readFile(b.file(p), true)
	if err != nil {
		t.Fatal(err)
	}
	return s.revision()
}
func definition(p, path string) Definition {
	d := Definition{Name: "demo", Path: path, ReadOnly: true}
	if p == "samba" {
		d.Users = []string{"alice", "@staff"}
		d.Browseable = true
	} else {
		d.Clients = []string{"10.1.2.0/24", "2001:db8::/64"}
		d.RootSquash = true
		d.Security = "sys"
	}
	return d
}
func change(t *testing.T, b *Backend, p string, d *Definition) {
	t.Helper()
	if _, err := b.Change(context.Background(), p, revision(t, b, p), "demo", d, io.Discard); err != nil {
		t.Fatal(err)
	}
}

func TestCreateEditRemovePreservesUnmanagedFilesAndDirectoryData(t *testing.T) {
	for _, p := range []string{"samba", "nfs"} {
		t.Run(p, func(t *testing.T) {
			b, r, path := fixture(t)
			samba, _ := os.ReadFile(b.sambaConfig)
			nfs, _ := os.ReadFile(b.nfsExports)
			put(t, filepath.Join(path, "keep.txt"), "unchanged")
			d := definition(p, path)
			initial := revision(t, b, p)
			change(t, b, p, &d)
			if revision(t, b, p) == initial {
				t.Fatal("revision did not advance")
			}
			stored, err := readFile(b.file(p), true)
			if err != nil {
				t.Fatal(err)
			}
			defs, err := load(p, stored)
			if err != nil || len(defs) != 1 {
				t.Fatal(defs, err)
			}
			inv, err := b.List(context.Background(), p)
			if err != nil {
				t.Fatal(err)
			}
			var found *Share
			for i := range inv.Shares {
				if inv.Shares[i].Managed {
					found = &inv.Shares[i]
				}
			}
			if found == nil || !found.Configured || found.Protocol != p {
				t.Fatal(inv)
			}
			if p == "nfs" && found.Runtime != "exported" {
				t.Fatal(*found)
			}
			if p == "samba" && (found.Connections == nil || *found.Connections != 1) {
				t.Fatal(*found)
			}
			serialized, _ := json.Marshal(inv)
			if strings.Contains(string(serialized), "PRIVATE") || strings.Contains(string(serialized), "SECRET") {
				t.Fatal("unselected Samba runtime fields leaked")
			}
			d.ReadOnly = false
			change(t, b, p, &d)
			change(t, b, p, nil)
			last, err := readFile(b.file(p), true)
			if err != nil {
				t.Fatal(err)
			}
			remaining, err := load(p, last)
			if err != nil || len(remaining) != 0 {
				t.Fatal(remaining, err)
			}
			gotSamba, _ := os.ReadFile(b.sambaConfig)
			gotNFS, _ := os.ReadFile(b.nfsExports)
			data, _ := os.ReadFile(filepath.Join(path, "keep.txt"))
			if string(gotSamba) != string(samba) || string(gotNFS) != string(nfs) || string(data) != "unchanged" {
				t.Fatal("unmanaged configuration/data changed")
			}
			if len(r.active()) != 1 || r.active()[0].path != "/legacy" {
				t.Fatal("unrelated runtime export changed", r.active())
			}
			for _, c := range r.recorded() {
				if c.Program == "exportfs" && slices.Contains(c.Args, "-ra") {
					t.Fatal("global NFS re-export requested")
				}
			}
			entries, _ := os.ReadDir(filepath.Dir(b.file(p)))
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), ".server-man-") {
					t.Fatal("temporary file leaked")
				}
			}
		})
	}
}

func TestSambaShadowValidationOccursBeforePublication(t *testing.T) {
	b, r, path := fixture(t)
	d := definition("samba", path)
	original := r.captureOverride
	r.captureOverride = func(c core.Command) (string, error) {
		if c.Program == "testparm" && c.Args[len(c.Args)-1] != b.sambaConfig {
			if _, err := os.Stat(b.sambaManaged); !os.IsNotExist(err) {
				t.Fatal("candidate published before validation")
			}
			return "", errors.New("invalid candidate")
		}
		if original != nil {
			return original(c)
		}
		return expandSamba(c.Args[len(c.Args)-1])
	}
	if _, err := b.Change(context.Background(), "samba", revision(t, b, "samba"), "demo", &d, io.Discard); err == nil {
		t.Fatal("invalid candidate accepted")
	}
	if _, err := os.Stat(b.sambaManaged); !os.IsNotExist(err) {
		t.Fatal("invalid config was published")
	}
	for _, c := range r.recorded() {
		if c.Program == "smbcontrol" {
			t.Fatal("invalid config reloaded")
		}
	}
}

func TestSambaRollbackOnReloadFailureAndCancellation(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelled), func(t *testing.T) {
			b, r, path := fixture(t)
			d := definition("samba", path)
			change(t, b, "samba", &d)
			old, _ := os.ReadFile(b.sambaManaged)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			r.beforeRun = func(runCtx context.Context, c core.Command) error {
				if c.Program == "smbcontrol" {
					calls++
					if calls == 1 {
						if cancelled {
							cancel()
							return runCtx.Err()
						}
						return errors.New("reload failed")
					}
					if runCtx.Err() != nil {
						t.Fatal("rollback inherited cancelled context")
					}
				}
				return nil
			}
			d.ReadOnly = false
			_, err := b.Change(ctx, "samba", revision(t, b, "samba"), "demo", &d, io.Discard)
			if err == nil || !strings.Contains(err.Error(), "restored") || calls != 2 {
				t.Fatal(err, calls)
			}
			got, _ := os.ReadFile(b.sambaManaged)
			if string(got) != string(old) {
				t.Fatal("previous file not restored")
			}
		})
	}
}

func TestNFSPartialFailureRestoresExactRuntimeAndFile(t *testing.T) {
	b, r, path := fixture(t)
	d := definition("nfs", path)
	change(t, b, "nfs", &d)
	old, _ := os.ReadFile(b.nfsManaged)
	before := r.active()
	d.ReadOnly = false
	d.Clients = []string{"10.1.3.0/24", "2001:db8::/64"}
	calls := 0
	r.afterRun = func(ctx context.Context, c core.Command) error {
		calls++
		if calls == 2 {
			return errors.New("partial apply failed after mutation")
		}
		return nil
	}
	_, err := b.Change(context.Background(), "nfs", revision(t, b, "nfs"), "demo", &d, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "restored") {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(b.nfsManaged)
	if string(got) != string(old) || !reflect.DeepEqual(r.active(), before) {
		t.Fatal("rollback did not restore original state", r.active(), before)
	}
}

func TestFirstCreateFailureRemovesFragmentAndNewExports(t *testing.T) {
	for _, p := range []string{"samba", "nfs"} {
		b, r, path := fixture(t)
		d := definition(p, path)
		calls := 0
		r.afterRun = func(context.Context, core.Command) error {
			calls++
			if calls == 1 {
				return errors.New("failure after apply")
			}
			return nil
		}
		if _, err := b.Change(context.Background(), p, revision(t, b, p), "demo", &d, io.Discard); err == nil {
			t.Fatal("apply failure hidden")
		}
		if _, err := os.Stat(b.file(p)); !os.IsNotExist(err) {
			t.Fatal("first-create rollback retained fragment")
		}
		if len(r.active()) != 1 {
			t.Fatal("first-create rollback retained new exports")
		}
	}
}

func TestStaleRevisionForeignFilesSymlinksAndFIFOs(t *testing.T) {
	b, r, path := fixture(t)
	d := definition("nfs", path)
	stale := revision(t, b, "nfs")
	change(t, b, "nfs", &d)
	count := len(r.recorded())
	if _, err := b.Change(context.Background(), "nfs", stale, "demo", &d, io.Discard); err == nil {
		t.Fatal("stale write accepted")
	}
	if len(r.recorded()) != count {
		t.Fatal("stale write executed commands")
	}
	put(t, b.nfsManaged, "# administrator data\n/path *(rw)\n")
	count = len(r.recorded())
	if _, err := b.Change(context.Background(), "nfs", revision(t, b, "nfs"), "demo", &d, io.Discard); err == nil {
		t.Fatal("foreign file replaced")
	}
	if len(r.recorded()) != count {
		t.Fatal("foreign file executed commands")
	}
	if err := os.Remove(b.nfsManaged); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(b.nfsExports, b.nfsManaged); err != nil {
		t.Fatal(err)
	}
	if _, err := readFile(b.nfsManaged, true); err == nil {
		t.Fatal("managed symlink followed")
	}
	pipe := filepath.Join(b.nfsDir, "pipe")
	if err := syscall.Mkfifo(pipe, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readFile(pipe, true); err == nil {
		t.Fatal("FIFO accepted")
	}
}

func TestExternalEditsAndRollbackFailuresAreReported(t *testing.T) {
	for _, external := range []bool{true, false} {
		b, r, path := fixture(t)
		d := definition("samba", path)
		change(t, b, "samba", &d)
		r.beforeRun = func(context.Context, core.Command) error {
			if external {
				put(t, b.sambaManaged, "external edit\n")
			}
			return errors.New("reload failure")
		}
		d.ReadOnly = false
		_, err := b.Change(context.Background(), "samba", revision(t, b, "samba"), "demo", &d, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "rollback incomplete") {
			t.Fatal(err)
		}
		if external {
			got, _ := os.ReadFile(b.sambaManaged)
			if string(got) != "external edit\n" {
				t.Fatal("external edit overwritten by rollback")
			}
		}
	}
}

func TestConflictsWithUnmanagedSharesAreRejected(t *testing.T) {
	b, _, path := fixture(t)
	d := definition("samba", path)
	d.Name = "legacy"
	if _, err := b.Change(context.Background(), "samba", revision(t, b, "samba"), "legacy", &d, io.Discard); err == nil {
		t.Fatal("unmanaged Samba collision accepted")
	}
	d = definition("nfs", path)
	put(t, b.nfsExports, strings.ReplaceAll(path, " ", `\040`)+" 10.1.2.0/24(ro)\n")
	if _, err := b.Change(context.Background(), "nfs", revision(t, b, "nfs"), "demo", &d, io.Discard); err == nil {
		t.Fatal("unmanaged NFS collision accepted")
	}
}

func TestValidationAndFragmentTampering(t *testing.T) {
	for _, name := range []string{"global", "homes", "printers", "../escape", "--flag", "bad[name]", "foo\nbar"} {
		if validName(name) {
			t.Fatal("invalid name", name)
		}
	}
	for _, path := range []string{"relative", "/a/../b", "/dir%U", "/foo\nread only=no", "/a#b", "/a\\b", "/a\"b"} {
		if validPath(path) {
			t.Fatal("invalid path", path)
		}
	}
	for _, value := range []string{"10.1.2.3", "10.1.2.3/24", "2001:db8::1", "2001:db8::/64", "*"} {
		if _, err := client(value); err != nil {
			t.Fatal(value, err)
		}
	}
	for _, value := range []string{"host(options)", "--all", "0.0.0.0/0(rw)", "10.0.0.1\n/path", "@netgroup", "example.com"} {
		if _, err := client(value); err == nil {
			t.Fatal("invalid client", value)
		}
	}
	d := definition("samba", "/shared")
	raw, err := render("samba", []Definition{d})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := load("samba", snapshot{exists: true, data: append(raw, []byte("extra = yes\n")...)}); err == nil {
		t.Fatal("outside edit accepted")
	}
	d.Users = []string{"alice\nroot"}
	if _, err := validate("samba", d, false); err == nil {
		t.Fatal("ACL injection accepted")
	}
	d = definition("nfs", "/shared")
	d.Security = "sys,rw"
	if _, err := validate("nfs", d, false); err == nil {
		t.Fatal("option injection accepted")
	}
	fields, err := parseExports("\"/space path\" 10.1.2.3(ro,sync)\n/other\\040path 2001:db8::/64(rw,sec=krb5p)\n")
	if err != nil || len(fields) != 2 || fields[0].path != "/space path" || fields[1].path != "/other path" || fields[0].client != "10.1.2.3/32" {
		t.Fatal(fields, err)
	}
	if target("2001:db8::/64", "/data") != "[2001:db8::]/64:/data" {
		t.Fatal("wrong IPv6 target")
	}
}

func TestRealTestparmValidatesTemporaryShadowConfiguration(t *testing.T) {
	program, err := exec.LookPath("testparm")
	if err != nil {
		t.Skip("testparm is not installed")
	}
	b, _, path := fixture(t)
	b.runner = core.Runner{}
	b.programs["testparm"] = program
	if _, err := b.sambaEffective(context.Background(), b.sambaConfig); err != nil {
		t.Fatal("first-use config with missing owned include failed", err)
	}
	d := definition("samba", path)
	guest := Definition{Name: "guests", Path: path, ReadOnly: true, Guest: true, Browseable: true}
	defs := []Definition{d, guest}
	data, err := render("samba", defs)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := temporary(filepath.Dir(b.sambaManaged), data, 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(candidate)
	for _, globals := range []string{"", "read only = no\nguest ok = yes\nvalid users = inherited\nbrowseable = no\n"} {
		put(t, b.sambaConfig, "[global]\n"+globals+"include = "+b.sambaManaged+"\n")
		if err := b.validateSamba(context.Background(), defs, nil, candidate); err != nil {
			t.Fatal(err)
		}
	}
	t.Log("Installed testparm validated a temporary Samba shadow; no daemon reload was executed")
}

func TestOverlappingUnmanagedNFSClientsRemainVisibleAndProtected(t *testing.T) {
	b, r, path := fixture(t)
	d := definition("nfs", path)
	change(t, b, "nfs", &d)
	extra := export{path: path, client: "10.88.0.0/16", options: []string{"ro", "sync", "no_subtree_check", "root_squash", "sec=sys"}}
	r.runtime[extra.key()] = extra
	inv, err := b.List(context.Background(), "nfs")
	if err != nil {
		t.Fatal(err)
	}
	visible := false
	for _, s := range inv.Shares {
		if !s.Managed && s.Path == path && slices.Contains(s.Clients, extra.client) {
			visible = true
		}
	}
	if !visible {
		t.Fatal("unmanaged runtime client hidden", inv.Shares)
	}
	put(t, b.nfsExports, strings.ReplaceAll(path, " ", `\040`)+" 10.1.2.0/24(ro)\n")
	before := r.active()
	if _, err := b.Change(context.Background(), "nfs", revision(t, b, "nfs"), "demo", nil, io.Discard); err == nil {
		t.Fatal("overlapping unmanaged export removed")
	}
	if !reflect.DeepEqual(before, r.active()) {
		t.Fatal("unmanaged runtime changed")
	}
}

func TestNativeRuntimeVerificationFailureTriggersRollback(t *testing.T) {
	b, r, path := fixture(t)
	d := definition("nfs", path)
	calls := 0
	r.afterRun = func(ctx context.Context, c core.Command) error {
		calls++
		if calls == 1 {
			r.mu.Lock()
			for key, e := range r.runtime {
				if e.path == path {
					e.options = []string{"rw", "insecure", "no_root_squash", "sec=sys"}
					r.runtime[key] = e
				}
			}
			r.mu.Unlock()
		}
		return nil
	}
	if _, err := b.Change(context.Background(), "nfs", revision(t, b, "nfs"), "demo", &d, io.Discard); err == nil || !strings.Contains(err.Error(), "restored") {
		t.Fatal(err)
	}
	if _, err := os.Stat(b.nfsManaged); !os.IsNotExist(err) {
		t.Fatal("mismatched runtime retained config")
	}
	if len(r.active()) != 1 {
		t.Fatal("mismatched runtime retained exports")
	}
}

func TestRegistryRequiresConfirmationAndPreservesWritePolicy(t *testing.T) {
	b, _, path := fixture(t)
	jobs, err := core.NewJobs(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer jobs.Close()
	r := core.NewRegistry(true, jobs)
	if err := b.RegisterDefinitions(r); err != nil {
		t.Fatal(err)
	}
	definitions, _ := json.Marshal([]Definition{definition("samba", path)})
	args := map[string]any{"protocol": "samba", "definitions": string(definitions), "revision": revision(t, b, "samba")}
	raw, _ := json.Marshal(args)
	if _, err := r.Invoke(context.Background(), "shares_definitions_apply", raw, "test"); err == nil {
		t.Fatal("confirmation bypassed")
	}
	args["confirm"] = true
	raw, _ = json.Marshal(args)
	result, err := r.Invoke(context.Background(), "shares_definitions_apply", raw, "test")
	if err != nil {
		t.Fatal(err)
	}
	id := result.(map[string]any)["job"].(core.Job).ID
	deadline := time.Now().Add(3 * time.Second)
	for {
		job, err := jobs.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if job.State != "running" {
			if job.State != "succeeded" {
				t.Fatal(job.Error)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("job timeout")
		}
		time.Sleep(time.Millisecond)
	}
	r.AllowWrites = false
	if _, err := r.Invoke(context.Background(), "shares_definitions_apply", raw, "test"); err == nil {
		t.Fatal("write policy bypassed")
	}
}
