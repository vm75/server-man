package declarative

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"strings"
	"syscall"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

type incusArchiveIndex struct {
	Name            string   `yaml:"name"`
	Pool            string   `yaml:"pool"`
	Backend         string   `yaml:"backend"`
	Type            string   `yaml:"type"`
	Optimized       *bool    `yaml:"optimized"`
	OptimizedHeader *bool    `yaml:"optimized_header"`
	Snapshots       []string `yaml:"snapshots"`
	Config          *struct {
		Container        *incusArchiveGuest    `yaml:"container"`
		Volume           *incusArchiveVolume   `yaml:"volume"`
		Snapshots        []*incusArchiveGuest  `yaml:"snapshots"`
		VolumeSnapshots  []*incusArchiveVolume `yaml:"volume_snapshots"`
		DependentVolumes []yaml.Node           `yaml:"dependent_volumes"`
		Pool             *struct {
			Name   string `yaml:"name"`
			Driver string `yaml:"driver"`
		} `yaml:"pool"`
	} `yaml:"config"`
}

type incusArchiveGuest struct {
	Name     string            `yaml:"name"`
	Type     string            `yaml:"type"`
	Config   map[string]string `yaml:"config"`
	Stateful bool              `yaml:"stateful"`
}
type incusArchiveVolume struct {
	Name        string `yaml:"name"`
	Type        string `yaml:"type"`
	ContentType string `yaml:"content_type"`
	Project     string `yaml:"project"`
}

type incusArchiveMember struct {
	kind byte
	link string
	size int64
}

type incusArchiveReader struct {
	io.Reader
	count int64
}

func (r *incusArchiveReader) Read(buffer []byte) (int, error) {
	n, err := r.Reader.Read(buffer)
	r.count += int64(n)
	return n, err
}
func zeroBackupBytes(buffer []byte) bool {
	for _, value := range buffer {
		if value != 0 {
			return false
		}
	}
	return true
}

// This scanner never extracts data or asks Incus to import it. It checks the
// complete uncompressed archive and its index; native restore policy is separate.
func verifyIncusArchive(ctx context.Context, root *os.Root, export BackupManifestNativeExport, cfg Config) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	invalid := errors.New("Incus archive structure, index identity or payload coverage did not verify")
	if export.Native == nil || export.Backend != "incus" {
		return invalid
	}
	// ISO exports are raw media, with no Incus index. Their bytes remain checked
	// by the shared verifier; native identity/import review remains required.
	parent, name, err := backupRealParent(root, export.BundlePath, false)
	if err != nil {
		return invalid
	}
	defer parent.Close()
	before, err := parent.Lstat(name)
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm()&0077 != 0 || before.Size() != export.SizeBytes || before.Size() <= 0 {
		return invalid
	}
	f, err := parent.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return invalid
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !stableBackupNode(before, opened) {
		return invalid
	}
	hash := sha256.New()
	reader := io.TeeReader(io.LimitReader(contextReader{ctx, f}, before.Size()), hash)
	var index incusArchiveIndex
	var members map[string]incusArchiveMember
	if export.Native.ContentType == "iso" {
		_, err = io.CopyN(io.Discard, reader, before.Size())
	} else {
		index, members, err = scanIncusArchive(ctx, reader)
	}
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return invalid
	}
	after, statErr := f.Stat()
	current, pathErr := root.Lstat(export.BundlePath)
	if statErr != nil || pathErr != nil || !stableBackupNode(before, after) || !stableBackupNode(before, current) || !backupParentStillReal(root, export.BundlePath, parent) || hex.EncodeToString(hash.Sum(nil)) != export.SHA256 {
		return invalid
	}
	if export.Native.ContentType != "iso" {
		if err := checkIncusArchiveIndex(index, members, export, cfg); err != nil {
			return invalid
		}
	}
	return ctx.Err()
}

func scanIncusArchive(ctx context.Context, reader io.Reader) (incusArchiveIndex, map[string]incusArchiveMember, error) {
	invalid := errors.New("invalid private Incus archive")
	var index incusArchiveIndex
	members := map[string]incusArchiveMember{}
	counted := &incusArchiveReader{Reader: reader}
	tarReader := tar.NewReader(counted)
	metadataBytes := 0
	for {
		if err := ctx.Err(); err != nil {
			return index, nil, err
		}
		start := counted.count
		h, err := tarReader.Next()
		if err == io.EOF {
			if counted.count-start < 1024 {
				return index, nil, invalid
			}
			break
		}
		if err != nil || len(members) >= maxBackupEntries {
			return index, nil, invalid
		}
		name := h.Name
		if h.Typeflag == tar.TypeDir {
			name = strings.TrimSuffix(name, "/")
		}
		if !backupRelativePath(name) || len(name) > 4096 || strings.Count(name, "/") > maxBackupDepth+4 || name != "backup" && !strings.HasPrefix(name, "backup/") || h.Size < 0 || h.Uid < 0 || h.Gid < 0 {
			return index, nil, invalid
		}
		if _, found := members[name]; found {
			return index, nil, invalid
		}
		metadataBytes += len(name) + len(h.Linkname)
		for key, value := range h.PAXRecords {
			metadataBytes += len(key) + len(value)
			if strings.HasPrefix(key, "GNU.sparse") {
				return index, nil, invalid
			}
		}
		if metadataBytes > maxBackupMetadataBytes {
			return index, nil, invalid
		}
		switch h.Typeflag {
		case tar.TypeReg, tar.TypeRegA:
			if h.Linkname != "" {
				return index, nil, invalid
			}
		case tar.TypeDir, tar.TypeSymlink, tar.TypeLink, tar.TypeChar, tar.TypeBlock, tar.TypeFifo:
			if h.Size != 0 {
				return index, nil, invalid
			}
			if h.Typeflag == tar.TypeSymlink && (!utf8.ValidString(h.Linkname) || strings.ContainsRune(h.Linkname, 0) || h.Linkname == "") {
				return index, nil, invalid
			}
			if h.Typeflag == tar.TypeLink && !backupRelativePath(h.Linkname) {
				return index, nil, invalid
			}
		default:
			return index, nil, invalid
		}
		members[name] = incusArchiveMember{h.Typeflag, h.Linkname, h.Size}
		if name == "backup/index.yaml" {
			if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA || h.Size == 0 || h.Size > MaxConfigBytes {
				return index, nil, invalid
			}
			raw, err := io.ReadAll(io.LimitReader(tarReader, MaxConfigBytes+1))
			if err != nil || len(raw) > MaxConfigBytes {
				return index, nil, invalid
			}
			if err := decodeIncusArchiveIndex(ctx, raw, &index); err != nil {
				return index, nil, invalid
			}
		} else {
			// Explicit draining detects truncation; GNU sparse expansion is refused
			// above, so logical work cannot exceed the physical stream budget.
			if _, err := io.CopyN(io.Discard, contextReader{ctx, tarReader}, h.Size); err != nil {
				return index, nil, invalid
			}
		}
	}
	if _, found := members["backup/index.yaml"]; !found {
		return index, nil, invalid
	}
	// Do not silently accept concatenated archives or garbage after the tar end.
	buffer := make([]byte, 32768)
	for {
		if err := ctx.Err(); err != nil {
			return index, nil, err
		}
		n, err := reader.Read(buffer)
		if n > 0 && !zeroBackupBytes(buffer[:n]) {
			return index, nil, invalid
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return index, nil, invalid
		}
	}
	for name, member := range members {
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			if p, found := members[parent]; found && p.kind != tar.TypeDir {
				return index, nil, invalid
			}
		}
		if member.kind == tar.TypeLink {
			target, found := members[member.link]
			if !found || target.kind != tar.TypeReg && target.kind != tar.TypeRegA {
				return index, nil, invalid
			}
		}
	}
	return index, members, ctx.Err()
}

func decodeIncusArchiveIndex(ctx context.Context, raw []byte, index *incusArchiveIndex) error {
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	var node yaml.Node
	if decoder.Decode(&node) != nil {
		return errors.New("invalid Incus index")
	}
	if len(node.Content) != 1 || node.Content[0].Kind != yaml.MappingNode {
		return errors.New("invalid Incus index")
	}
	if err := checkYAMLStructure(ctx, &node, 64, 65536); err != nil {
		return err
	}
	var extra yaml.Node
	if decoder.Decode(&extra) != io.EOF {
		return errors.New("invalid Incus index document count")
	}
	return node.Decode(index)
}

func checkIncusArchiveIndex(index incusArchiveIndex, members map[string]incusArchiveMember, export BackupManifestNativeExport, cfg Config) error {
	invalid := errors.New("Incus index does not match the reviewed object")
	object := export.Native
	if index.Name != object.Name || index.Pool != object.Pool || index.Optimized == nil || *index.Optimized || index.OptimizedHeader == nil || *index.OptimizedHeader || index.Config == nil || index.Backend == "" || len(index.Snapshots) > 2048 {
		return invalid
	}
	if pool := index.Config.Pool; pool != nil && (pool.Name != object.Pool || pool.Driver != index.Backend) {
		return invalid
	}
	volumeType := "custom"
	if export.Kind == "incus_instance_export" {
		volumeType = "virtual-machine"
	}
	volume := index.Config.Volume
	if volume == nil || volume.Name != object.Name || volume.Type != volumeType || volume.ContentType != object.ContentType || volume.Project != "" && volume.Project != object.Project || len(index.Config.DependentVolumes) != 0 {
		return invalid
	}
	prefix, snapshotPrefix := "volume", "volume-snapshots"
	if export.Kind == "incus_instance_export" {
		prefix, snapshotPrefix = "virtual-machine", "virtual-machine-snapshots"
		instance := index.Config.Container
		if index.Type != "virtual-machine" || instance == nil || index.Config.Pool == nil || instance.Name != object.Name || instance.Type != "virtual-machine" || len(export.Resources) != 1 {
			return invalid
		}
		id := strings.TrimPrefix(export.Resources[0], "service/")
		service, found := cfg.Services[id]
		if !found || service.VM == nil || !incusArchiveGuestOwned(instance, id, service.VM.UUID) {
			return invalid
		}
	} else {
		if export.Kind != "incus_custom_volume" || index.Type != "custom" {
			return invalid
		}
	}
	block := object.ContentType == "block"
	required := func(base string) bool {
		member, found := members["backup/"+base]
		if !found {
			return false
		}
		if block {
			return (member.kind == tar.TypeReg || member.kind == tar.TypeRegA) && member.size > 0
		}
		return member.kind == tar.TypeDir
	}
	base := prefix
	if block {
		base += ".img"
	}
	if !required(base) {
		return invalid
	}
	snapshots := map[string]bool{}
	for _, name := range index.Snapshots {
		if !bindingNamePattern.MatchString(name) || snapshots[name] {
			return invalid
		}
		snapshots[name] = true
		relative := snapshotPrefix + "/" + name
		if block {
			relative += ".img"
		}
		if !required(relative) {
			return invalid
		}
	}
	if !incusSnapshotRecordsMatch(index, snapshots, export, cfg) {
		return invalid
	}
	directoryRoots := map[string]bool{}
	blockFiles := map[string]bool{}
	addPayload := func(base string) error {
		if block {
			blockFiles["backup/"+base+".img"] = true
		}
		if !block || export.Kind == "incus_instance_export" {
			root := "backup/" + base
			if member, found := members[root]; !found || member.kind != tar.TypeDir {
				return invalid
			}
			directoryRoots[root] = true
		}
		return nil
	}
	if err := addPayload(prefix); err != nil {
		return err
	}
	for snapshot := range snapshots {
		if err := addPayload(snapshotPrefix + "/" + snapshot); err != nil {
			return err
		}
	}
	section := func(name string) string {
		for at := name; at != "."; at = path.Dir(at) {
			if directoryRoots[at] {
				return at
			}
		}
		return ""
	}
	for name, member := range members {
		if name == "backup/index.yaml" || blockFiles[name] {
			continue
		}
		if (name == "backup" || name == "backup/"+snapshotPrefix) && member.kind == tar.TypeDir {
			continue
		}
		root := section(name)
		if root == "" || member.kind == tar.TypeLink && section(member.link) != root {
			return invalid
		}
	}
	return nil
}
