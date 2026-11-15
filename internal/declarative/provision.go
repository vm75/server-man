package declarative

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"server-man/internal/core"
)

type Provisioner struct {
	Coordinator Coordinator
	Runner      core.Executor
	QemuImg     string
}
type ProvisionPlan struct {
	Source             *DiskSource `json:"source,omitempty"`
	SourceVirtualBytes int64       `json:"sourceVirtualBytes,omitempty"`
	Storage            string      `json:"storage"`
	Revision           string      `json:"revision"`
	Path               string      `json:"path"`
	Kind               string      `json:"kind"`
	Format             string      `json:"format,omitempty"`
	SizeGiB            int         `json:"sizeGiB"`
	AvailableBytes     uint64      `json:"availableBytes"`
	UID                int         `json:"uid"`
	GID                int         `json:"gid"`
	Mode               string      `json:"mode"`
	Consumers          []string    `json:"consumers"`
	ReviewToken        string      `json:"reviewToken"`
}

func NewProvisioner(c Coordinator, runner core.Executor) Provisioner {
	program, _ := exec.LookPath("qemu-img")
	return Provisioner{c, runner, program}
}

func storageConsumers(cfg Config, id string) []string {
	consumers := []string{}
	for serviceID, service := range cfg.Services {
		found := slices.Contains(serviceDataReferences(service), "storage/"+id)
		if found {
			consumers = append(consumers, "service/"+serviceID)
		}
	}
	for shareID, share := range cfg.Shares {
		if share.Storage == id {
			consumers = append(consumers, "share/"+shareID)
		}
	}
	slices.Sort(consumers)
	return consumers
}

// A held directory handle confines all new data; symlinks and writable-by-other
// parents are refused rather than turning a review into an arbitrary path write.
func (p Provisioner) prepare(ctx context.Context, id, expected string, sizeGiB int) (ProvisionPlan, *os.Root, error) {
	c := p.Coordinator
	snapshot, err := c.Store.Read()
	if err != nil {
		return ProvisionPlan{}, nil, errors.New("repair configuration before provisioning data")
	}
	if snapshot.Revision != expected {
		return ProvisionPlan{}, nil, ErrConflict
	}
	storage, ok := snapshot.Config.Storage[id]
	if !ok {
		return ProvisionPlan{}, nil, errors.New("unknown storage reference")
	}
	if storage.External || !portablePath(storage.Path) || !filepath.IsAbs(c.Store.Path) {
		return ProvisionPlan{}, nil, errors.New("new data must use an owned relative storage path")
	}
	manifest := filepath.Base(c.Store.Path)
	first := strings.Split(storage.Path, string(filepath.Separator))[0]
	if first == manifest || strings.HasPrefix(first, manifest+".") || strings.HasPrefix(first, ".server-man-") {
		return ProvisionPlan{}, nil, errors.New("storage overlaps private configuration or generated state")
	}
	for _, path := range snapshot.Config.Secrets {
		if path == storage.Path {
			return ProvisionPlan{}, nil, errors.New("storage overlaps a private-file reference")
		}
	}
	kind := storage.Kind
	if kind == "" {
		kind = "directory"
	}
	if kind == "directory" {
		if sizeGiB != 0 {
			return ProvisionPlan{}, nil, errors.New("directory provisioning does not accept a disk size")
		}
	} else if sizeGiB < 1 || sizeGiB > 4096 || (storage.Format != "raw" && storage.Format != "qcow2") {
		return ProvisionPlan{}, nil, errors.New("new disks require raw or qcow2 format and size from 1 to 4096 GiB")
	}
	if kind == "file" && (storage.Format == "qcow2" || storage.Source != nil) && (p.QemuImg == "" || p.Runner == nil) {
		return ProvisionPlan{}, nil, errors.New("qemu-img is required to provision qcow2 disks; install it explicitly and restart server-man")
	}
	root, err := os.OpenRoot(filepath.Dir(c.Store.Path))
	if err != nil {
		return ProvisionPlan{}, nil, errors.New("configuration directory cannot be opened")
	}
	defer root.Close()
	parent := filepath.Dir(storage.Path)
	parts := []string{"."}
	if parent != "." {
		at := ""
		for _, part := range strings.Split(parent, string(filepath.Separator)) {
			at = filepath.Join(at, part)
			parts = append(parts, at)
		}
	}
	for _, part := range parts {
		info, e := root.Lstat(part)
		if e != nil || !info.IsDir() {
			return ProvisionPlan{}, nil, errors.New("every parent must already be a real directory; create or restore the parent explicitly first")
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || int(stat.Uid) != os.Geteuid() || info.Mode().Perm()&0022 != 0 {
			return ProvisionPlan{}, nil, errors.New("provisioning parents must belong to the application account and deny group/other writes")
		}
	}
	directory, err := root.OpenRoot(parent)
	if err != nil {
		return ProvisionPlan{}, nil, errors.New("storage parent cannot be opened")
	}
	success := false
	defer func() {
		if !success {
			directory.Close()
		}
	}()
	name := filepath.Base(storage.Path)
	if _, err = directory.Lstat(name); !os.IsNotExist(err) {
		return ProvisionPlan{}, nil, errors.New("storage path already exists or cannot be checked; existing data is never replaced")
	}
	handle, err := directory.Open(".")
	if err != nil {
		return ProvisionPlan{}, nil, err
	}
	defer handle.Close()
	info, err := handle.Stat()
	if err != nil {
		return ProvisionPlan{}, nil, err
	}
	stat := info.Sys().(*syscall.Stat_t)
	var fs syscall.Statfs_t
	if err = syscall.Fstatfs(int(handle.Fd()), &fs); err != nil || fs.Flags&1 != 0 {
		return ProvisionPlan{}, nil, errors.New("storage parent filesystem is unavailable or read-only")
	}
	available := fs.Bavail * uint64(fs.Bsize)
	if available < 16<<20 {
		return ProvisionPlan{}, nil, errors.New("storage parent needs at least 16 MiB free for provisioning metadata; sparse disk capacity is not reserved")
	}
	source := ""
	if storage.Mount != "" {
		f, e := os.Open("/proc/self/mountinfo")
		if e != nil {
			return ProvisionPlan{}, nil, errors.New("expected filesystem mount cannot be checked")
		}
		mounts, e := ParseMountInfo(f)
		f.Close()
		real, e2 := filepath.EvalSymlinks(filepath.Join(filepath.Dir(c.Store.Path), parent))
		if e != nil || e2 != nil || !filepath.IsAbs(storage.Mount) || mountedSource(real, mounts) != storage.Mount {
			return ProvisionPlan{}, nil, errors.New("expected filesystem is not mounted; provisioning must not create replacement data on another filesystem")
		}
		source = storage.Mount
	}
	consumers := storageConsumers(snapshot.Config, id)
	for _, key := range consumers {
		kind, resourceID, _ := strings.Cut(key, "/")
		if kind == "share" {
			if snapshot.Config.Shares[resourceID].Enabled {
				return ProvisionPlan{}, nil, errors.New("unpublish referencing shares before provisioning new data")
			}
			adapter := c.Adapters[backend(snapshot.Config, key)]
			if adapter == nil {
				return ProvisionPlan{}, nil, errors.New("unpublished share state cannot be checked")
			}
			observed, e := adapter.Inspect(ctx, snapshot.Config, key)
			if e != nil || !observed.Matches {
				return ProvisionPlan{}, nil, errors.New("referencing shares must be verified unpublished before provisioning new data")
			}
			continue
		}
		adapter := c.Adapters[backend(snapshot.Config, key)]
		if adapter == nil {
			return ProvisionPlan{}, nil, errors.New("referencing service state cannot be checked")
		}
		observed, e := adapter.Inspect(ctx, snapshot.Config, key)
		if e != nil || observed.Active {
			return ProvisionPlan{}, nil, errors.New("all referencing services must be verified stopped before provisioning new data")
		}
	}
	if err = ctx.Err(); err != nil {
		return ProvisionPlan{}, nil, err
	}
	mode := "0700"
	if kind == "file" {
		mode = "0600"
	}
	gid := os.Getegid()
	if info.Mode()&os.ModeSetgid != 0 {
		gid = int(stat.Gid)
	}
	plan := ProvisionPlan{Storage: id, Revision: expected, Path: c.Store.Resolve(storage.Path), Kind: kind, Format: storage.Format, SizeGiB: sizeGiB, AvailableBytes: available, UID: os.Geteuid(), GID: gid, Mode: mode, Consumers: consumers, Source: storage.Source}
	var sourceIdentity any
	if storage.Source != nil {
		source, image, e := p.openSource(ctx, *storage.Source)
		if e != nil {
			return ProvisionPlan{}, nil, e
		}
		sourceInfo, e := source.Stat()
		source.Close()
		if e != nil {
			return ProvisionPlan{}, nil, e
		}
		if image.VirtualSize > int64(sizeGiB)<<30 {
			return ProvisionPlan{}, nil, errors.New("destination capacity is smaller than the source image")
		}
		plan.SourceVirtualBytes = image.VirtualSize
		st := sourceInfo.Sys().(*syscall.Stat_t)
		sourceIdentity = []any{st.Dev, st.Ino, sourceInfo.Size(), sourceInfo.ModTime().UnixNano(), storage.Source.SHA256}
	}
	token, _ := json.Marshal([]any{expected, id, plan.Path, kind, storage.Format, sizeGiB, stat.Dev, stat.Ino, source, plan.UID, plan.GID, p.QemuImg, sourceIdentity})
	plan.ReviewToken = digest(token)
	success = true
	return plan, directory, nil
}
func (p Provisioner) Plan(ctx context.Context, id, revision string, sizeGiB int) (ProvisionPlan, error) {
	plan, parent, err := p.prepare(ctx, id, revision, sizeGiB)
	if parent != nil {
		parent.Close()
	}
	return plan, err
}
func (p Provisioner) Create(ctx context.Context, id, revision, reviewToken string, sizeGiB int) (result ProvisionPlan, resultErr error) {
	if err := p.Coordinator.checkMutationReady(); err != nil {
		return result, err
	}
	plan, parent, err := p.prepare(ctx, id, revision, sizeGiB)
	if err != nil {
		return plan, err
	}
	defer parent.Close()
	if reviewToken != plan.ReviewToken {
		return plan, errors.New("provisioning conditions changed; review the plan again")
	}
	// An external editor is outside the job lock: recheck before writing any data.
	_, current, err := p.Coordinator.Store.ReadRaw()
	if err != nil || current != revision {
		return plan, ErrConflict
	}
	name := filepath.Base(plan.Path)
	if err = ctx.Err(); err != nil {
		return plan, err
	}
	if plan.Kind == "directory" {
		if err = p.verifyPlan(ctx, plan); err != nil {
			return plan, err
		}
		if err = parent.Mkdir(name, 0700); err != nil {
			return plan, errors.New("new directory could not be created; existing paths are never replaced")
		}
	} else {
		bytes := make([]byte, 16)
		if _, err = rand.Read(bytes); err != nil {
			return plan, err
		}
		temp := ".server-man-new-storage-" + hex.EncodeToString(bytes)
		f, e := parent.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if e != nil {
			return plan, errors.New("new disk staging file cannot be created")
		}
		defer f.Close()
		defer func() {
			if cleanupErr := parent.Remove(temp); cleanupErr != nil && !os.IsNotExist(cleanupErr) {
				resultErr = errors.Join(resultErr, errors.New("new disk staging cleanup failed; inspect the storage parent before retrying"))
			}
		}()
		if plan.Format == "raw" {
			err = f.Truncate(int64(sizeGiB) << 30)
		} else {
			err = p.createQCOW2(ctx, f, sizeGiB)
		}
		if err != nil {
			return plan, errors.New("new disk creation or image verification failed; no destination was published")
		}
		if plan.Source != nil {
			if err = p.cloneSource(ctx, f, plan); err != nil {
				return plan, fmt.Errorf("image provisioning failed; no destination was published: %w", err)
			}
		}
		if err = f.Sync(); err != nil {
			return plan, errors.New("new disk could not be synced; no destination was published")
		}
		if err = ctx.Err(); err != nil {
			return plan, err
		}
		if err = p.verifyPlan(ctx, plan); err != nil {
			return plan, err
		}
		if err = parent.Link(temp, name); err != nil {
			return plan, errors.New("new disk could not be published; existing paths are never replaced")
		}
		if err = parent.Remove(temp); err != nil {
			return plan, errors.New("data created but staging cleanup failed; inspect the path before retrying")
		}
	}
	handle, err := parent.Open(".")
	if err != nil {
		return plan, errors.New("data created but parent sync failed; inspect the path before retrying")
	}
	defer handle.Close()
	if err = handle.Sync(); err != nil {
		return plan, errors.New("data created but parent sync failed; inspect the path before retrying")
	}
	return plan, nil
}
func (p Provisioner) verifyPlan(ctx context.Context, reviewed ProvisionPlan) error {
	latest, err := p.Plan(ctx, reviewed.Storage, reviewed.Revision, reviewed.SizeGiB)
	if err != nil {
		return err
	}
	if latest.ReviewToken != reviewed.ReviewToken {
		return errors.New("provisioning conditions changed; review the plan again")
	}
	return nil
}
func (p Provisioner) createQCOW2(ctx context.Context, f *os.File, sizeGiB int) error {
	// Native tools receive a held, newly created file descriptor through procfs.
	// They never receive an existing user data path, even if directories move.
	path := descriptorPath(f)
	if err := p.Runner.Run(ctx, core.Command{Program: p.QemuImg, Args: []string{"create", "-f", "qcow2", "-o", "preallocation=off", path, fmt.Sprintf("%dG", sizeGiB)}, Timeout: 2 * time.Minute}, io.Discard); err != nil {
		return err
	}
	image, err := p.inspectImage(ctx, f, "qcow2")
	if err != nil || image.VirtualSize != int64(sizeGiB)<<30 {
		return errors.New("new disk verification failed")
	}
	return nil
}
