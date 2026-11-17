package declarative

import (
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "os"
    "path/filepath"
    "strings"
    "testing"
)

func incusArtifactRecoveryFixture(t *testing.T) (Coordinator, *Incus, Config, string) {
    t.Helper()
    adapter, cfg, _ := incusFixture(t)
    if err := adapter.Apply(context.Background(), Config{Version: 1}, cfg, "service/guest", "now"); err != nil { t.Fatal(err) }
    saved, err := adapter.Store.Save(cfg, "missing")
    if err != nil { t.Fatal(err) }
    c := Coordinator{Store: adapter.Store, Adapters: map[string]Adapter{"incus": adapter}}
    key := "service/guest"
    j := Journal{Version: 1, BaseRevision: "missing", DesiredRevision: saved.Revision, Mode: "now", Stage: "failed",
        Resources: map[string]ResourceResult{key: {Fingerprint: resourceFingerprint(saved.Config, key), State: "failed", Message: artifactConflictRemedy}}}
    if err := c.writeJournal(&j); err != nil { t.Fatal(err) }
    return c, adapter, saved.Config, saved.Revision
}

func TestReviewedArtifactReconciliationPreservesSidecarAndNativeFiles(t *testing.T) {
    c, adapter, _, revision := incusArtifactRecoveryFixture(t)
    native := filepath.Join(adapter.resourceDir("service/guest"), "instance.yaml")
    before, err := os.ReadFile(native)
    if err != nil { t.Fatal(err) }
    manifest := artifactManifestPath(adapter.resourceDir("service/guest"))
    other := artifactManifestFor(map[string][]byte{"instance.yaml": []byte(ownedHeader + "unrelated\n")})
    drifted, err := json.Marshal(other)
    if err != nil { t.Fatal(err) }
    if err := os.WriteFile(manifest, drifted, 0600); err != nil { t.Fatal(err) }
    review, err := c.ReviewArtifactRecovery("service/guest")
    if err != nil || !review.CanReconcile || review.Revision != revision || len(review.Parts) != 1 || review.Parts[0].Manifest != "conflicting" || review.Parts[0].Files != "desired" {
        t.Fatal("valid-looking unrelated sidecar not reviewable", review, err)
    }
    if _, err := c.ReconcileArtifactMetadata(context.Background(), "service/guest", revision, strings.Repeat("a", 64)); !errors.Is(err, ErrConflict) {
        t.Fatal("unreviewed token accepted", err)
    }
    if _, err := c.ReconcileArtifactMetadata(context.Background(), "service/guest", strings.Repeat("a", 64), review.ReviewToken); !errors.Is(err, ErrConflict) {
        t.Fatal("stale desired revision accepted", err)
    }
    after, err := c.ReconcileArtifactMetadata(context.Background(), "service/guest", revision, review.ReviewToken)
    if err != nil || after.CanReconcile || after.Parts[0].Manifest != "current" {
        t.Fatal("manifest reconciliation did not complete", after, err)
    }
    preserved := filepath.Join(filepath.Dir(manifest), filepath.Base(manifest)+".preserved-"+digest(drifted))
    if got, err := os.ReadFile(preserved); err != nil || !bytes.Equal(got, drifted) {
        t.Fatal("old sidecar was not retained privately", err)
    }
    if info, err := os.Stat(preserved); err != nil || info.Mode().Perm()&0077 != 0 {
        t.Fatal("preserved sidecar is not owner-only", err)
    }
    if got, err := os.ReadFile(native); err != nil || !bytes.Equal(got, before) {
        t.Fatal("metadata reconciliation modified native definition", err)
    }
    journal, err := c.ReadJournal()
    if err != nil || journal.Stage != "failed" {
        t.Fatal("metadata-only reconciliation falsely completed apply", err, journal.Stage)
    }
    journal, err = c.Retry(context.Background())
    if err != nil || journal.Stage != "complete" {
        t.Fatal("explicit native retry did not resolve journal", err, journal.Stage)
    }
}

func TestArtifactReviewRejectsNativeDriftAndUnsafeSidecars(t *testing.T) {
    for _, scenario := range []string{"foreign_native", "symlink_manifest", "public_manifest"} {
        t.Run(scenario, func(t *testing.T) {
            c, adapter, _, revision := incusArtifactRecoveryFixture(t)
            native := filepath.Join(adapter.resourceDir("service/guest"), "instance.yaml")
            sidecar := artifactManifestPath(adapter.resourceDir("service/guest"))
            switch scenario {
            case "foreign_native":
                if err := os.WriteFile(native, []byte(ownedHeader+"foreign\n"), 0600); err != nil { t.Fatal(err) }
            case "symlink_manifest":
                if err := os.Remove(sidecar); err != nil { t.Fatal(err) }
                if err := os.Symlink(native, sidecar); err != nil { t.Fatal(err) }
            case "public_manifest":
                if err := os.Chmod(sidecar, 0644); err != nil { t.Fatal(err) }
            }
            review, err := c.ReviewArtifactRecovery("service/guest")
            if err != nil || review.CanReconcile {
                t.Fatal("unsafe repair was advertised", review, err)
            }
            if _, err := c.ReconcileArtifactMetadata(context.Background(), "service/guest", revision, review.ReviewToken); !errors.Is(err, ErrConflict) {
                t.Fatal("unsafe repair was executed", err)
            }
        })
    }
}

func TestArtifactReconciliationRejectsChangedReviewAndCancellation(t *testing.T) {
    c, adapter, _, revision := incusArtifactRecoveryFixture(t)
    sidecar := artifactManifestPath(adapter.resourceDir("service/guest"))
    if err := os.WriteFile(sidecar, []byte("broken"), 0600); err != nil { t.Fatal(err) }
    review, err := c.ReviewArtifactRecovery("service/guest")
    if err != nil || !review.CanReconcile { t.Fatal("safe private corrupt sidecar cannot be reviewed", review, err) }
    if err := os.WriteFile(sidecar, []byte("changed"), 0600); err != nil { t.Fatal(err) }
    if _, err := c.ReconcileArtifactMetadata(context.Background(), "service/guest", revision, review.ReviewToken); !errors.Is(err, ErrConflict) {
        t.Fatal("changed private manifest accepted", err)
    }
    review, err = c.ReviewArtifactRecovery("service/guest")
    if err != nil || !review.CanReconcile { t.Fatal(review, err) }
    ctx, cancel := context.WithCancel(context.Background()); cancel()
    if _, err := c.ReconcileArtifactMetadata(ctx, "service/guest", revision, review.ReviewToken); !errors.Is(err, context.Canceled) {
        t.Fatal("cancelled job modified metadata", err)
    }
    if got, err := os.ReadFile(sidecar); err != nil || string(got) != "changed" {
        t.Fatal("cancelled repair modified sidecar", err)
    }
    if _, err := c.ReconcileArtifactMetadata(context.Background(), "service/guest", revision, review.ReviewToken); err != nil {
        t.Fatal("fresh review did not reconcile safe corrupt manifest", err)
    }
}

func TestArtifactRecoveryNeverRepairsShareOrUnrecordedConflict(t *testing.T) {
    c, _, _, _ := incusArtifactRecoveryFixture(t)
    for _, key := range []string{"share/example", "service/foreign", "service/../invalid"} {
        if _, err := c.ReviewArtifactRecovery(key); err == nil {
            t.Fatal("unrecorded or unsafe recovery accepted", key)
        }
    }
}

func TestArtifactRecoveryComposeChecksBothNativePublicationTargets(t *testing.T) {
    adapter, cfg, _ := composeFixture(t)
    key := "service/website"
    if err := adapter.Apply(context.Background(), Config{Version: 1}, cfg, key, "now"); err != nil { t.Fatal(err) }
    saved, err := adapter.Store.Save(cfg, "missing")
    if err != nil { t.Fatal(err) }
    c := Coordinator{Store: adapter.Store, Adapters: map[string]Adapter{"podman/compose": adapter}}
    j := Journal{Version: 1, BaseRevision: "missing", DesiredRevision: saved.Revision, Mode: "next_start", Stage: "failed",
        Resources: map[string]ResourceResult{key: {Fingerprint: resourceFingerprint(saved.Config, key), State: "failed", Message: artifactConflictRemedy}}}
    if err := c.writeJournal(&j); err != nil { t.Fatal(err) }
    bootDir := filepath.Dir(adapter.unitFile(key))
    bootSidecar := artifactManifestPath(bootDir)
    bootOriginal, err := os.ReadFile(bootSidecar)
    if err != nil { t.Fatal(err) }
    boot, err := os.ReadFile(adapter.unitFile(key))
    if err != nil { t.Fatal(err) }
    if err := os.WriteFile(bootSidecar, []byte("corrupt private boot sidecar"), 0600); err != nil { t.Fatal(err) }
    review, err := c.ReviewArtifactRecovery(key)
    if err != nil || !review.CanReconcile || len(review.Parts) != 2 ||
       review.Parts[0].Files != "desired" || review.Parts[0].Manifest != "current" ||
       review.Parts[1].Files != "desired" || review.Parts[1].Manifest != "conflicting" {
        t.Fatal("compose partial-sidecar damage not isolated", review, err)
    }
    if _, err := c.ReconcileArtifactMetadata(context.Background(), key, saved.Revision, review.ReviewToken); err != nil { t.Fatal(err) }
    if got, err := os.ReadFile(bootSidecar); err != nil || !bytes.Equal(got, bootOriginal) {
        t.Fatal("boot sidecar was not restored", err)
    }
    if got, err := os.ReadFile(adapter.unitFile(key)); err != nil || !bytes.Equal(got, boot) {
        t.Fatal("boot launcher was changed", err)
    }
    // An unrelated native edit on the *other* target must block a future
    // metadata reconciliation even when the boot sidecar alone is damaged.
    if err := os.WriteFile(filepath.Join(adapter.resourceDir(key), "compose.yaml"), []byte("foreign"), 0600); err != nil { t.Fatal(err) }
    if err := os.WriteFile(bootSidecar, []byte("another damaged sidecar"), 0600); err != nil { t.Fatal(err) }
    review, err = c.ReviewArtifactRecovery(key)
    if err != nil || review.CanReconcile || review.Parts[0].Files != "mismatch_or_unsafe" {
        t.Fatal("native Compose content drift was ignored", review, err)
    }
}

func TestArtifactRecoveryCapsPreservedMetadataWithoutDeletingIt(t *testing.T) {
    c, adapter, _, revision := incusArtifactRecoveryFixture(t)
    sidecar := artifactManifestPath(adapter.resourceDir("service/guest"))
    for i := 0; i < maxPreservedPublicationManifests; i++ {
        value := []byte{byte(i)}
        name := filepath.Base(sidecar) + ".preserved-" + digest(value)
        if err := os.WriteFile(filepath.Join(filepath.Dir(sidecar), name), value, 0600); err != nil { t.Fatal(err) }
    }
    raw := []byte("fresh conflicting sidecar")
    if err := os.WriteFile(sidecar, raw, 0600); err != nil { t.Fatal(err) }
    review, err := c.ReviewArtifactRecovery("service/guest")
    if err != nil || review.CanReconcile || review.Parts[0].Manifest != "preservation_limit" {
        t.Fatal("metadata archive growth was not bounded", review, err)
    }
    if _, err := c.ReconcileArtifactMetadata(context.Background(), "service/guest", revision, review.ReviewToken); !errors.Is(err, ErrConflict) {
        t.Fatal("preservation cap bypassed", err)
    }
    old, err := os.ReadFile(sidecar)
    if err != nil || !bytes.Equal(old, raw) { t.Fatal("rejected repair overwrote sidecar", err) }
    if err := os.Remove(filepath.Join(filepath.Dir(sidecar), filepath.Base(sidecar)+".preserved-"+digest([]byte{0}))); err != nil { t.Fatal(err) }
    review, err = c.ReviewArtifactRecovery("service/guest")
    if err != nil || !review.CanReconcile { t.Fatal("review did not recover after explicit archive cleanup", review, err) }
}

func TestArtifactTargetsRenderAllSupportedBackendsWithoutPublishing(t *testing.T) {
    quadC, quadCfg, _ := coordinatorFixture(t)
    quad := &Quadlet{Store:quadC.Store, Directory:filepath.Join(t.TempDir(), "quadlet")}
    compose, composeCfg, _ := composeFixture(t)
    libvirt, libvirtCfg, _ := libvirtFixture(t)
    incus, incusCfg, _ := incusFixture(t)
    cases := []struct {
        adapter Adapter
        config Config
        key string
        count int
    }{
        {quad, quadCfg, "service/website", 1},
        {compose, composeCfg, "service/website", 2},
        {libvirt, libvirtCfg, "service/guest", 1},
        {incus, incusCfg, "service/guest", 1},
    }
    for _, item := range cases {
        targets, err := renderedArtifactTargets(item.adapter, Config{Version:1}, item.config, item.key)
        if err != nil || len(targets) != item.count { t.Fatal("adapter artifact renderer failed", item.key, err) }
        for _, target := range targets {
            if len(target.desired) == 0 { t.Fatal("empty generated artifact selection") }
            if _, err := os.Stat(target.dir); !os.IsNotExist(err) {
                t.Fatal("read-only renderer created native artifact directory", target.dir, err)
            }
        }
    }
}

func TestRetryCannotClaimSuccessWithDriftedArtifactPublication(t *testing.T) {
    c, adapter, _, revision := incusArtifactRecoveryFixture(t)
    sidecar := artifactManifestPath(adapter.resourceDir("service/guest"))
    if err := os.WriteFile(sidecar, []byte("changed private metadata"), 0600); err != nil { t.Fatal(err) }
    // Native Incus is already registered and matches the desired VM; old
    // code would skip Apply and mark the entire failed journal complete.
    review, err := c.ReviewArtifactRecovery("service/guest")
    if err != nil || !review.CanReconcile { t.Fatal(review, err) }
    j, err := c.Retry(context.Background())
    if err == nil || j.Stage != "failed" {
        t.Fatal("native observation alone falsely completed publication", err, j.Stage)
    }
    if got, err := os.ReadFile(sidecar); err != nil || string(got) != "changed private metadata" {
        t.Fatal("retry overwrote the conflicting artifact", err)
    }
    if _, err := c.ReconcileArtifactMetadata(context.Background(), "service/guest", revision, review.ReviewToken); err != nil { t.Fatal(err) }
    j, err = c.Retry(context.Background())
    if err != nil || j.Stage != "complete" { t.Fatal("repaired metadata still blocked explicit retry", err, j.Stage) }
}

func TestPublicationFastPathDetectsMissingAndStaleSidecar(t *testing.T) {
    _, adapter, cfg, _ := incusArtifactRecoveryFixture(t)
    key := "service/guest"
    if !artifactPublicationMatchesDesired(adapter, cfg, key) { t.Fatal("fresh publication not detected") }
    path := artifactManifestPath(adapter.resourceDir(key))
    data, err := os.ReadFile(path)
    if err != nil { t.Fatal(err) }
    if err := os.Remove(path); err != nil { t.Fatal(err) }
    if artifactPublicationMatchesDesired(adapter, cfg, key) { t.Fatal("missing sidecar treated as committed publication") }
    if err := os.WriteFile(path, data, 0600); err != nil { t.Fatal(err) }
    native := filepath.Join(adapter.resourceDir(key), "instance.yaml")
    if err := os.WriteFile(native, []byte(ownedHeader+"outside edit\n"), 0600); err != nil { t.Fatal(err) }
    if artifactPublicationMatchesDesired(adapter, cfg, key) { t.Fatal("native definition drift treated as valid publication") }
}

func TestArtifactReviewBoundsNativeFileInventory(t *testing.T) {
    c, adapter, _, _ := incusArtifactRecoveryFixture(t)
    dir := adapter.resourceDir("service/guest")
    for i := 0; i < 257; i++ {
        name := filepath.Join(dir, fmt.Sprintf("unknown-%03d", i))
        if err := os.WriteFile(name, []byte("foreign"), 0600); err != nil { t.Fatal(err) }
    }
    review, err := c.ReviewArtifactRecovery("service/guest")
    if err != nil || review.CanReconcile || review.Parts[0].Files != "mismatch_or_unsafe" {
        t.Fatal("unbounded or foreign generated directory treated as safe", review, err)
    }
}
