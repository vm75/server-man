package declarative

import (
	"context"
	"encoding/json"
	"errors"
)

// Compare inventoried dependencies across the stopped capture boundary, ignoring
// observed running/stopped state and file sizes which naturally change on stop.
// This detects changed attachments; it is not an exclusive lock on outside CLIs.
func nativeCaptureSignature(inventory BackupInventory) string {
	type identity struct {
		ID, Kind, Strategy, Path, IncludedBy string
		Resources                            []string
		Native                               *NativeBackupObject
	}
	var objects []identity
	for _, input := range inventory.Inputs {
		if input.Strategy == "native_export" || input.Strategy == "native_file" || input.Strategy == "external_data" {
			objects = append(objects, identity{input.ID, input.Kind, input.Strategy, input.Path, input.IncludedBy, input.Resources, input.Native})
		}
	}
	raw, _ := json.Marshal(objects)
	return digest(raw)
}
func (c Coordinator) verifyNativeCaptureBoundary(ctx context.Context, expected BackupInventory) error {
	needed := false
	for _, input := range expected.Inputs {
		needed = needed || input.Strategy == "native_export"
	}
	if !needed {
		return nil
	}
	current, err := c.BackupInventory(ctx)
	if err != nil {
		return err
	}
	if current.Revision != expected.Revision {
		return ErrConflict
	}
	for _, issue := range current.Issues {
		if issue.Severity != "warning" {
			return errors.New("native backup dependencies became unresolved during capture")
		}
	}
	for _, input := range current.Inputs {
		if input.Strategy == "native_export" && input.RuntimeState != "" && input.RuntimeState != "stopped" {
			return errors.New("native workload is not stopped at the capture boundary")
		}
	}
	if nativeCaptureSignature(current) != nativeCaptureSignature(expected) {
		return errors.New("native backup attachments changed; review inventory again")
	}
	return nil
}
