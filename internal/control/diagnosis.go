package control

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
	"github.com/ryancswallace/jobman-dashboard/internal/reports"
	"github.com/ryancswallace/jobman/diagnostic"
)

func (c *Client) DiagnosticSnapshot(ctx context.Context, a monitoring.Actor, selection diagnostic.SharedSelection) (reports.Snapshot, error) {
	var out reports.Snapshot
	if selection.DeploymentID != c.ID() || selection.ControlInstanceID != c.config.InstanceID || !uuid(selection.NamespaceID) || !uuid(selection.JobID) || selection.RunID != "" && !uuid(selection.RunID) || selection.ExpectedJobRevision > 9223372036854775807 {
		return out, monitoring.ErrNotFound
	}
	scope := api.Scope{DeploymentID: c.ID(), NamespaceID: selection.NamespaceID}
	d, n, err := c.authorize(ctx, a, scope, "evidence.read")
	if err != nil {
		return out, err
	}
	if !slices.Contains(d.features, "shared-diagnostic-snapshots") {
		return out, &api.Error{Code: "unsupported_contract", Message: "This Jobman Control deployment does not support diagnosis reports. Ask your administrator about upgrading it."}
	}
	params := url.Values{"deploymentId": {selection.DeploymentID}, "controlInstanceId": {selection.ControlInstanceID}, "namespaceId": {selection.NamespaceID}}
	if selection.RunID != "" {
		params.Set("runId", selection.RunID)
	}
	if selection.ExpectedJobRevision != 0 {
		params.Set("expectedJobRevision", strconv.FormatUint(selection.ExpectedJobRevision, 10))
	}
	var response struct {
		targetAuthority
		Snapshot json.RawMessage `json:"snapshot"`
	}
	if err = c.get(ctx, a, "evidence.read", n.ID, "/v1/namespaces/"+n.Name+"/jobs/"+selection.JobID+"/diagnostic-snapshot", params, &response); err != nil {
		return out, err
	}
	v, decodeErr := diagnostic.DecodeSharedSnapshot(bytes.NewReader(response.Snapshot))
	if decodeErr != nil || !exactSnapshotFields(response.Snapshot) {
		return out, monitoring.ErrSource
	}
	if !c.validTargetAuthority(response.targetAuthority, d, n, "DiagnosticSnapshot") || !v.CapturedAt.Equal(response.AsOf) || v.Source.DeploymentID != selection.DeploymentID || v.Source.ControlInstanceID != selection.ControlInstanceID || v.Source.NamespaceID != selection.NamespaceID || v.Job.ID != selection.JobID || selection.ExpectedJobRevision != 0 && v.Job.Revision != selection.ExpectedJobRevision || selection.RunID != "" && (len(v.Runs) != 1 || v.Runs[0].ID != selection.RunID) {
		return out, monitoring.ErrSource
	}
	if err = c.recheck(ctx, a, d, n, "evidence.read"); err != nil {
		return out, err
	}
	return reports.Snapshot{Value: v, RecoveryEpoch: response.RecoveryEpoch}, nil
}

// Keep the public decoder's duplicate/depth/byte checks before this structural
// pass. encoding/json otherwise accepts case aliases of a versioned field name.
// Raw fact values retain their own schema and custom scalar types retain their
// public decoder validation. This function never returns source content.
func exactSnapshotFields(encoded []byte) bool {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return false
	}
	return exactSnapshotField(value, reflect.TypeFor[diagnostic.SharedSnapshot]())
}

func exactSnapshotField(value any, schema reflect.Type) bool {
	if reflect.PointerTo(schema).Implements(reflect.TypeFor[json.Unmarshaler]()) {
		return true
	}
	if schema.Kind() == reflect.Pointer {
		return exactSnapshotField(value, schema.Elem())
	}
	switch schema.Kind() {
	case reflect.Struct:
		object, ok := value.(map[string]any)
		if !ok {
			return value == nil
		}
		fields := make(map[string]reflect.Type, schema.NumField())
		for index := range schema.NumField() {
			field := schema.Field(index)
			name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
			if name != "" && name != "-" {
				fields[name] = field.Type
			}
		}
		for name, child := range object {
			field, exists := fields[name]
			if !exists || !exactSnapshotField(child, field) {
				return false
			}
		}
	case reflect.Slice, reflect.Array:
		if values, ok := value.([]any); ok {
			for _, child := range values {
				if !exactSnapshotField(child, schema.Elem()) {
					return false
				}
			}
		}
	}
	return true
}
