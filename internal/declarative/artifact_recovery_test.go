package declarative

import (
    "bytes"
    "context"
    "encoding/json"
    "errors"
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
