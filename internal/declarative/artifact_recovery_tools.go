package declarative

import (
    "context"
    "io"

    "server-man/internal/core"
)

// Reviewed publication-metadata reconciliation is separate from Retry. A
// review never modifies the native filesystem; the confirmed job preserves
// conflicting owner-private metadata without touching generated definitions.
func RegisterArtifactRecovery(r *core.Registry, c Coordinator) error {
    resource := core.StringField("Failed managed service resource, e.g. service/example", true, 80)
    private := func() error {
        if !r.AllowWrites {
            return &core.Error{Code: "writes_disabled", Status: 403, Message: "Private artifact recovery is disabled with management writes."}
        }
        return nil
    }
    return r.AddModule(core.Module{ID: "managed_artifact_recovery", Title: "Generated artifact recovery", Available: true},
        core.Tool{Name: "managed_artifact_review", Title: "Review generated artifact conflict", Description: "Inspect the current failed publication, exact previous/desired native bytes and private sidecar state without exposing generated file contents or paths. Unsafe or drifted native files cannot be reconciled automatically.", ReadOnly: true, Fields: map[string]core.Field{"resource": resource}, Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
            if err := private(); err != nil { return nil, err }
            review, err := c.ReviewArtifactRecovery(in.String("resource"))
            return review, managedError(err)
        }},
        core.Tool{Name: "managed_artifact_reconcile", Title: "Preserve and reconcile generated-artifact metadata", Description: "After a separate review, preserve any existing private conflicting publication sidecar and atomically restore only its canonical metadata. Never overwrites native generated definitions, changes workloads or automatically retries apply. Requires revision, resource and exact on-disk review token.", Destructive: true, Fields: map[string]core.Field{
            "resource": resource,
            "revision": core.StringField("Pinned current configuration revision", true, 64),
            "reviewToken": core.StringField("Token from the most recent artifact review", true, 64),
        }, Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
            review, err := c.ReconcileArtifactMetadata(ctx, in.String("resource"), in.String("revision"), in.String("reviewToken"))
            return review, managedError(err)
        }},
    )
}
