package declarative

import (
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "io"
    "os"
    "path/filepath"
    "strings"
    "syscall"
)

type ArtifactRecoveryPart struct {
    Label string `json:"label"`
    Files string `json:"files"`
    Manifest string `json:"manifest"`
}
type ArtifactRecoveryReview struct {
    Resource string `json:"resource"`
    Revision string `json:"revision"`
    ReviewToken string `json:"reviewToken"`
    CanReconcile bool `json:"canReconcile"`
    Parts []ArtifactRecoveryPart `json:"parts"`
}

type artifactTarget struct {
    label, dir string
    previous, desired map[string][]byte
}
type artifactReconcilePart struct {
    target artifactTarget
    expected []byte
    original []byte
    present bool
    change bool
}

func renderedArtifactTargets(adapter Adapter, previous, desired Config, key string) ([]artifactTarget, error) {
    _, id, _ := strings.Cut(key, "/")
    _, had := previous.Services[id]
    _, exists := desired.Services[id]
    if !exists {
        return nil, errors.New("removed resources require manual recovery inspection")
    }
    switch a := adapter.(type) {
    case *Quadlet:
        oldFiles, newFiles := map[string][]byte{}, map[string][]byte{}
        var err error
        if had {
            oldFiles, err = RenderQuadlet(a.Store, previous, key)
            if err != nil { return nil, err }
        }
        newFiles, err = RenderQuadlet(a.Store, desired, key)
        if err != nil { return nil, err }
        return []artifactTarget{{"Native Quadlet definitions", a.resourceDir(key), oldFiles, newFiles}}, nil
    case *Compose:
        oldFiles, newFiles := map[string][]byte{}, map[string][]byte{}
        oldBoot, newBoot := map[string][]byte{}, map[string][]byte{}
        if had {
            data, err := RenderCompose(a.Store, previous, key)
            if err != nil { return nil, err }
            oldFiles["compose.yaml"] = data
            unit, err := RenderComposeBoot(previous, key, a.Program, a.file(key))
            if err != nil { return nil, err }
            oldBoot[composeUnit(id)] = unit
        }
        data, err := RenderCompose(a.Store, desired, key)
        if err != nil { return nil, err }
        newFiles["compose.yaml"] = data
        unit, err := RenderComposeBoot(desired, key, a.Program, a.file(key))
        if err != nil { return nil, err }
        newBoot[composeUnit(id)] = unit
        return []artifactTarget{
            {"Native Compose definition", a.resourceDir(key), oldFiles, newFiles},
            {"Native boot launcher", filepath.Dir(a.unitFile(key)), oldBoot, newBoot},
        }, nil
    case *Libvirt:
        oldFiles := map[string][]byte{}
        if had {
            data, err := RenderLibvirt(a.Store, previous, key)
            if err != nil { return nil, err }
            oldFiles["domain.xml"] = data
        }
        data, err := RenderLibvirt(a.Store, desired, key)
        if err != nil { return nil, err }
        return []artifactTarget{{"Native libvirt definition", a.resourceDir(key), oldFiles, map[string][]byte{"domain.xml": data}}}, nil
    case *Incus:
        oldFiles := map[string][]byte{}
        if had {
            definition, err := RenderIncus(previous, key)
            if err != nil { return nil, err }
            data, err := encodeIncus(definition)
            if err != nil { return nil, err }
            oldFiles["instance.yaml"] = data
        }
        definition, err := RenderIncus(desired, key)
        if err != nil { return nil, err }
        data, err := encodeIncus(definition)
        if err != nil { return nil, err }
        return []artifactTarget{{"Native Incus definition", a.resourceDir(key), oldFiles, map[string][]byte{"instance.yaml": data}}}, nil
    default:
        return nil, errors.New("this backend does not publish local generated artifacts")
    }
}

// The review refuses foreign native bytes or unsafe directory entries. It
// compares complete file sets, not just names or the recorded manifest hash.
// Returned metadata never contains paths, native definitions or private values.
func matchedArtifactBytes(t artifactTarget) (map[string][]byte, string, error) {
    info, err := os.Lstat(t.dir)
    if err != nil { return nil, "", err }
    if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
        return nil, "", errors.New("unsafe native artifact directory")
    }
    root, err := os.OpenRoot(t.dir)
    if err != nil { return nil, "", err }
    defer root.Close()
    directory, err := root.Open(".")
    if err != nil { return nil, "", err }
    defer directory.Close()
    entries, err := directory.ReadDir(-1)
    if err != nil { return nil, "", err }
    if len(entries) > 256 { return nil, "", errors.New("too many native artifacts") }
    actual := make(map[string][]byte, len(entries))
    for _, entry := range entries {
        content, err := readArtifact(root, entry.Name())
        if err != nil { return nil, "", err }
        actual[entry.Name()] = content
    }
    equal := func(reference map[string][]byte) bool {
        if len(actual) != len(reference) { return false }
        for name, content := range reference {
            found, ok := actual[name]
            if !ok || !bytes.Equal(content, found) { return false }
        }
        return true
    }
    if len(t.desired) != 0 && equal(t.desired) { return t.desired, "desired", nil }
    if len(t.previous) != 0 && equal(t.previous) { return t.previous, "previous", nil }
    return nil, "different", nil
}

// A repairable sidecar must be a bounded, owner-only, single-link regular file
// or absent. Symlinks, other file types, public modes and hardlinks are not
// repaired automatically, even with user approval.
func readReconcileSidecar(dir string) ([]byte, bool, error) {
    path := artifactManifestPath(dir)
    root, err := os.OpenRoot(filepath.Dir(path))
    if err != nil { return nil, false, err }
    defer root.Close()
    f, err := root.OpenFile(filepath.Base(path), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
    if os.IsNotExist(err) { return nil, false, nil }
    if err != nil { return nil, false, err }
    defer f.Close()
    info, err := f.Stat()
    if err != nil { return nil, false, err }
    native, ok := info.Sys().(*syscall.Stat_t)
    if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > MaxConfigBytes || !ok || native.Nlink != 1 {
        return nil, false, errors.New("unsafe artifact sidecar")
    }
    data, err := io.ReadAll(io.LimitReader(f, MaxConfigBytes+1))
    if err != nil || len(data) > MaxConfigBytes { return nil, false, errors.New("artifact sidecar cannot be read safely") }
    return data, true, nil
}

func (c Coordinator) artifactRecoveryState(resource string) (ArtifactRecoveryReview, []artifactReconcilePart, error) {
    if !strings.HasPrefix(resource, "service/") || !validID(strings.TrimPrefix(resource, "service/")) {
        return ArtifactRecoveryReview{}, nil, errors.New("invalid service resource")
    }
    j, err := c.ReadJournal()
    if err != nil { return ArtifactRecoveryReview{}, nil, errors.New("a failed apply journal is required for artifact recovery") }
    result, exists := j.Resources[resource]
    if j.Stage != "failed" || !exists || result.State != "failed" || result.Message != artifactConflictRemedy {
        return ArtifactRecoveryReview{}, nil, errors.New("resource has no recorded generated-artifact conflict")
    }
    raw, revision, err := c.Store.ReadRaw()
    if err != nil { return ArtifactRecoveryReview{}, nil, errors.New("current configuration cannot be read") }
    if revision != j.DesiredRevision { return ArtifactRecoveryReview{}, nil, ErrConflict }
    desired, err := Parse(raw)
    if err != nil { return ArtifactRecoveryReview{}, nil, errors.New("current configuration is invalid") }
    if resourceFingerprint(desired, resource) != result.Fingerprint {
        return ArtifactRecoveryReview{}, nil, ErrConflict
    }
    previous := Config{Version: 1}
    if j.BaseRevision != "missing" {
        historical, err := c.Store.ReadHistory(j.BaseRevision)
        if err != nil || digest(historical) != j.BaseRevision {
            return ArtifactRecoveryReview{}, nil, errors.New("prior configuration history is unavailable")
        }
        previous = previousConfiguration(historical)
    }
    adapter := c.Adapters[backend(desired, resource)]
    targets, err := renderedArtifactTargets(adapter, previous, desired, resource)
    if err != nil { return ArtifactRecoveryReview{}, nil, errors.New("native generated-artifact review is unavailable") }
    review := ArtifactRecoveryReview{Resource: resource, Revision: revision, Parts: []ArtifactRecoveryPart{}}
    parts := make([]artifactReconcilePart, 0, len(targets))
    // The token includes the complete private journal and observed content
    // fingerprints; it is not a password or an authorization substitute.
    seal := []any{revision, j, resource}
    allRepairable, changed := true, false
    for _, target := range targets {
        part := ArtifactRecoveryPart{Label: target.label, Files: "unverified", Manifest: "unverified"}
        internal := artifactReconcilePart{target: target}
        contents, version, err := matchedArtifactBytes(target)
        part.Files = version
        if err == nil && contents != nil {
            internal.expected, err = json.Marshal(artifactManifestFor(contents))
        }
        if err != nil || contents == nil {
            part.Files = "mismatch_or_unsafe"
            allRepairable = false
        } else {
            old, present, readErr := readReconcileSidecar(target.dir)
            if readErr != nil {
                part.Manifest = "unsafe"
                allRepairable = false
            } else {
                internal.original, internal.present = old, present
                if !present { part.Manifest = "missing" } else if bytes.Equal(old, internal.expected) { part.Manifest = "current" } else { part.Manifest = "conflicting" }
                internal.change = part.Manifest != "current"
                changed = changed || internal.change
            }
        }
        seal = append(seal, part, digest(internal.expected), digest(internal.original))
        parts = append(parts, internal)
        review.Parts = append(review.Parts, part)
    }
    review.CanReconcile = allRepairable && changed
    encoded, _ := json.Marshal(seal)
    review.ReviewToken = digest(encoded)
    return review, parts, nil
}

// Review only diagnoses generated files. It never reads private artifact bytes
// into the API output and never probes or mutates a native backend.
func (c Coordinator) ReviewArtifactRecovery(resource string) (ArtifactRecoveryReview, error) {
    review, _, err := c.artifactRecoveryState(resource)
    return review, err
}

// Atomic metadata-only repair: preserve any conflicting sidecar separately
// before installing canonical metadata. It never replaces a native definition.
// Interrupted work leaves the apply journal failed and may be re-reviewed.
func reconcileSidecar(part artifactReconcilePart) error {
    path := artifactManifestPath(part.target.dir)
    parent, err := os.OpenRoot(filepath.Dir(path))
    if err != nil { return err }
    defer parent.Close()
    name := filepath.Base(path)
    latest, present, err := readReconcileSidecar(part.target.dir)
    if err != nil || present != part.present || !bytes.Equal(latest, part.original) {
        return ErrConflict
    }
    // A second exact-byte check prevents repair after an outside edit to
    // native files between review and publication.
    matched, _, err := matchedArtifactBytes(part.target)
    if err != nil || matched == nil {
        return ErrConflict
    }
    canonical, err := json.Marshal(artifactManifestFor(matched))
    if err != nil || !bytes.Equal(canonical, part.expected) {
        return ErrConflict
    }
    if present {
        backup := name + ".preserved-" + digest(part.original)
        f, err := parent.OpenFile(backup, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
        if err != nil {
            if !os.IsExist(err) { return err }
            // A prior interrupted attempt may have preserved the same bytes.
            saved, savedErr := readReconcileBackup(parent, backup)
            if savedErr != nil || !bytes.Equal(saved, part.original) { return ErrConflict }
        } else {
            if _, err = f.Write(part.original); err == nil { err = f.Sync() }
            closeErr := f.Close()
            if err != nil { return err }
            if closeErr != nil { return closeErr }
        }
        if err := syncArtifactParent(parent); err != nil { return err }
    }
    temp := "." + name + ".reconcile"
    f, err := parent.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
    if err != nil { return err }
    defer parent.Remove(temp)
    if _, err = f.Write(canonical); err == nil { err = f.Sync() }
    closeErr := f.Close()
    if err != nil { return err }
    if closeErr != nil { return closeErr }
    latest, present, err = readReconcileSidecar(part.target.dir)
    if err != nil || present != part.present || !bytes.Equal(latest, part.original) {
        return ErrConflict
    }
    if err := parent.Rename(temp, name); err != nil { return err }
    return syncArtifactParent(parent)
}
func readReconcileBackup(root *os.Root, name string) ([]byte, error) {
    f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
    if err != nil { return nil, err }
    defer f.Close()
    info, err := f.Stat()
    if err != nil { return nil, err }
    native, ok := info.Sys().(*syscall.Stat_t)
    if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > MaxConfigBytes || !ok || native.Nlink != 1 {
        return nil, errors.New("unsafe preserved artifact sidecar")
    }
    return io.ReadAll(io.LimitReader(f, MaxConfigBytes+1))
}
func syncArtifactParent(root *os.Root) error {
    d, err := root.Open(".")
    if err != nil { return err }
    defer d.Close()
    return d.Sync()
}

// ReconcileArtifactMetadata is a destructive, locked management job whose
// explicit review token pins config, failed journal and on-disk bytes. Only
// metadata is replaced; the operator separately retries normal native apply.
func (c Coordinator) ReconcileArtifactMetadata(ctx context.Context, resource, revision, token string) (ArtifactRecoveryReview, error) {
    if err := c.checkMutationReady(); err != nil { return ArtifactRecoveryReview{}, err }
    review, parts, err := c.artifactRecoveryState(resource)
    if err != nil { return ArtifactRecoveryReview{}, err }
    if !review.CanReconcile || review.Revision != revision || review.ReviewToken != token {
        return ArtifactRecoveryReview{}, ErrConflict
    }
    for _, part := range parts {
        if err := ctx.Err(); err != nil { return ArtifactRecoveryReview{}, err }
        if part.change {
            if err := reconcileSidecar(part); err != nil {
                return ArtifactRecoveryReview{}, errors.New("publication metadata changed or could not be preserved; inspect the retained failed journal and re-review")
            }
        }
    }
    // No automatic retries, native validation, workload activation or journal
    // completion. The operator must review the resulting status and retry.
    updated, _, err := c.artifactRecoveryState(resource)
    return updated, err
}
