package components

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/elva-labs/terraform-provider-vew/internal/provider/testhelpers"
	"github.com/elva-labs/terraform-provider-vew/internal/providerdata"
	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	vewcomponents "github.com/elva-labs/terraform-provider-vew/internal/vew/components"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// immediateVersionWaiter keeps the real HTTP/domain/resource boundary while
// replacing only the slow polling clock. Shared waiter timing has its own tests.
type immediateVersionWaiter struct {
	timeout, delay time.Duration
	limit          int
	afterRead      func()
}

func (w *immediateVersionWaiter) Until(ctx context.Context, timeout, delay time.Duration, read vew.StatusReader, evaluate vew.StatusEvaluator) error {
	w.timeout, w.delay = timeout, delay
	for n := 0; n < 20; n++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		result, err := read(ctx)
		if err != nil {
			return err
		}
		if w.afterRead != nil {
			w.afterRead()
		}
		if done, err := evaluate(result.Status); done || err != nil {
			return err
		}
		if w.limit > 0 && n+1 >= w.limit {
			return &vew.TimeoutError{LastStatus: result.Status}
		}
	}
	return &vew.TimeoutError{}
}

func versionFixture(status string) vewcomponents.ComponentVersion {
	return vewcomponents.ComponentVersion{ID: "version", ComponentID: "component", Description: "description", Name: "1.0.0", Definition: json.RawMessage(`{"phases":[]}`), Dependencies: []vewcomponents.Dependency{}, SoftwareVendor: "vendor", SoftwareVersion: "1.0", Status: status, CreatedAt: "2026-09-22T00:00:00Z", CreatedBy: "creator", UpdatedAt: "2026-09-22T00:00:00Z", UpdatedBy: "updater"}
}

func versionHarness(t *testing.T, statuses ...string) (*componentVersionResource, *testhelpers.VEWServer, *immediateVersionWaiter) {
	t.Helper()
	fake := testhelpers.NewVEWServer(t)
	responses := make([]testhelpers.VersionResponse, len(statuses))
	for i, status := range statuses {
		responses[i] = testhelpers.VersionResponse{Version: versionFixture(status)}
	}
	fake.QueueVersionReads("project", "component", "version", responses...)
	w := &immediateVersionWaiter{}
	return &componentVersionResource{client: fake.ComponentVersionAPI(t), waiter: w}, fake, w
}

func createVersion(t *testing.T, ctx context.Context, r *componentVersionResource, model componentVersionModel) resource.CreateResponse {
	t.Helper()
	s := componentVersionState(t, r, model)
	response := resource.CreateResponse{State: tfsdk.State{Schema: s.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan{Raw: s.Raw, Schema: s.Schema}}, &response)
	return response
}

func decodeVersionState(t *testing.T, state tfsdk.State) componentVersionModel {
	t.Helper()
	var model componentVersionModel
	if d := state.Get(context.Background(), &model); d.HasError() {
		t.Fatalf("decode state: %v", d)
	}
	return model
}

func assertVersionMethods(t *testing.T, fake *testhelpers.VEWServer, want ...string) {
	t.Helper()
	var got []string
	for _, request := range fake.VersionRequests() {
		got = append(got, request.Method)
		path := "/projects/project/components/component/versions/version"
		if request.Method == "POST" {
			path = "/projects/project/components/component/versions"
		}
		if request.Path != path {
			t.Errorf("path = %q, want %q", request.Path, path)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("methods = %v, want %v", got, want)
	}
}

func TestComponentVersionCreateWaitsThroughCreatedAndTestingToValidated(t *testing.T) {
	r, fake, w := versionHarness(t, "CREATING", "CREATED", "TESTING", "VALIDATED")
	fake.SetVersionRetryAfter("3")
	model := validComponentVersionModel(t)
	model.ID = types.StringUnknown()
	model.Timeouts = timeoutsValue(t, "2m", "", "")
	response := createVersion(t, context.Background(), r, model)
	if response.Diagnostics.HasError() {
		t.Fatal(response.Diagnostics)
	}
	got := decodeVersionState(t, response.State)
	model.ID = types.StringValue("version")
	if !response.State.Raw.Equal(componentVersionState(t, r, model).Raw) {
		t.Fatalf("state = %#v, want %#v", got, model)
	}
	assertVersionMethods(t, fake, "POST", "GET", "GET", "GET", "GET")
	if w.timeout != 2*time.Minute || w.delay != 3*time.Second {
		t.Fatalf("wait timeout/delay = %v/%v", w.timeout, w.delay)
	}
	if fake.VersionRequests()[0].IdempotencyKey == "" {
		t.Fatal("missing idempotency key")
	}
}

func TestComponentVersionCreateUsesNormalizedDefinitionAndSortedDependencies(t *testing.T) {
	r, fake, _ := versionHarness(t, "VALIDATED")
	model := validComponentVersionModel(t)
	model.DefinitionJSON = types.StringValue(`{"phases":[{"steps":[{"name":"install"}]}]}`)
	model.Dependencies = dependencyList(
		dependencyModel{ComponentID: types.StringValue("b"), ComponentName: types.StringValue("B"), VersionID: types.StringValue("vb"), VersionName: types.StringValue("2"), Type: types.StringNull(), Order: types.Int64Value(2), Position: types.StringNull()},
		dependencyModel{ComponentID: types.StringValue("a"), ComponentName: types.StringValue("A"), VersionID: types.StringValue("va"), VersionName: types.StringValue("1"), Type: types.StringValue("MAIN"), Order: types.Int64Value(1), Position: types.StringNull()},
	)
	response := createVersion(t, context.Background(), r, model)
	if response.Diagnostics.HasError() {
		t.Fatal(response.Diagnostics)
	}
	var input vewcomponents.CreateComponentVersionInput
	if err := json.Unmarshal(fake.VersionRequests()[0].Body, &input); err != nil {
		t.Fatal(err)
	}
	if string(input.Definition) != `{"phases":[{"steps":[{"maxAttempts":1,"name":"install","onFailure":"Abort","timeoutSeconds":7200}]}]}` || len(input.Dependencies) != 2 || input.Dependencies[0].ComponentID != "a" || input.Dependencies[1].Type != "HELPER" || input.ReleaseType != "MAJOR" {
		t.Fatalf("unexpected create payload: %#v", input)
	}
}

func TestComponentVersionCreatePreservesProvisionalStateOnFailedStatus(t *testing.T) {
	for _, status := range []string{"FAILED", "RELEASED", "RETIRED", "definition_json secret"} {
		t.Run(status, func(t *testing.T) {
			r, _, _ := versionHarness(t, "CREATING", status)
			response := createVersion(t, context.Background(), r, validComponentVersionModel(t))
			assertRecoverableVersion(t, response.State, response.Diagnostics, status)
		})
	}
}

func assertRecoverableVersion(t *testing.T, state tfsdk.State, diagnostics diag.Diagnostics, status string) {
	t.Helper()
	if !diagnostics.HasError() || diagnosticsContain(diagnostics, "definition_json") || diagnosticsContain(diagnostics, "secret") {
		t.Fatalf("unsafe or missing diagnostics: %v", diagnostics)
	}
	got := decodeVersionState(t, state)
	if got.ID.ValueString() != "version" || got.ProjectID.ValueString() != "project" || got.ComponentID.ValueString() != "component" || got.ReleaseType.ValueString() != "MAJOR" || got.Status.ValueString() != status {
		t.Fatalf("lost recoverable state: %#v", got)
	}
}

func TestComponentVersionCreatePreservesProvisionalStateOnTimeout(t *testing.T) {
	r, _, w := versionHarness(t, "TESTING")
	w.limit = 1
	response := createVersion(t, context.Background(), r, validComponentVersionModel(t))
	assertRecoverableVersion(t, response.State, response.Diagnostics, "TESTING")
}

func TestComponentVersionCreatePreservesLastSuccessfulObservationOnMalformedLaterPoll(t *testing.T) {
	for _, malformed := range []struct {
		name       string
		definition json.RawMessage
	}{
		{"invalid definition", json.RawMessage(`"definition_json secret"`)},
		{"missing definition", nil},
	} {
		t.Run(malformed.name, func(t *testing.T) {
			r, fake, _ := versionHarness(t)
			drift := versionFixture("TESTING")
			drift.Definition = json.RawMessage(`{"remote":"retained"}`)
			drift.Dependencies = []vewcomponents.Dependency{{ComponentID: "a", ComponentName: "A", VersionID: "va", VersionName: "1", Type: "HELPER", Order: 1}}
			drift.Description = "successful observation"
			invalid := versionFixture("VALIDATED")
			invalid.Definition = malformed.definition
			invalid.Description = "incomplete observation"
			fake.QueueVersionReads("project", "component", "version", testhelpers.VersionResponse{Version: drift}, testhelpers.VersionResponse{Version: invalid})
			response := createVersion(t, context.Background(), r, validComponentVersionModel(t))
			if !response.Diagnostics.HasError() || diagnosticsContain(response.Diagnostics, "definition_json") || diagnosticsContain(response.Diagnostics, "secret") {
				t.Fatalf("unsafe or missing diagnostics: %v", response.Diagnostics)
			}
			got := decodeVersionState(t, response.State)
			want := validComponentVersionModel(t)
			want.Status, want.Description = types.StringValue("TESTING"), types.StringValue("successful observation")
			want.DefinitionJSON = types.StringValue(`{"remote":"retained"}`)
			want.Dependencies = dependencyList(dependencyModel{ComponentID: types.StringValue("a"), ComponentName: types.StringValue("A"), VersionID: types.StringValue("va"), VersionName: types.StringValue("1"), Type: types.StringValue("HELPER"), Order: types.Int64Value(1), Position: types.StringNull()})
			if !response.State.Raw.Equal(componentVersionState(t, r, want).Raw) {
				t.Fatalf("lost last successful observation: status=%s, description=%s, definition=%s, dependencies=%s", got.Status, got.Description, got.DefinitionJSON, got.Dependencies)
			}
			assertVersionMethods(t, fake, "POST", "GET", "GET")
		})
	}
}

func TestComponentVersionCreatePreservesStateOnCancellationAndReadError(t *testing.T) {
	t.Run("cancellation", func(t *testing.T) {
		r, _, w := versionHarness(t, "TESTING")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		w.afterRead = cancel
		response := createVersion(t, ctx, r, validComponentVersionModel(t))
		assertRecoverableVersion(t, response.State, response.Diagnostics, "TESTING")
	})
	t.Run("first read fails", func(t *testing.T) {
		r, fake, _ := versionHarness(t)
		fake.QueueVersionReads("project", "component", "version", testhelpers.VersionResponse{StatusCode: 400, Detail: "definition_json secret"})
		response := createVersion(t, context.Background(), r, validComponentVersionModel(t))
		assertRecoverableVersion(t, response.State, response.Diagnostics, "CREATING")
	})
}

func TestComponentVersionResourceMetadata(t *testing.T) {
	r := NewComponentVersionResource()
	var response resource.MetadataResponse
	r.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "vew"}, &response)
	if response.TypeName != "vew_component_version" {
		t.Fatalf("type name = %q, want %q", response.TypeName, "vew_component_version")
	}
}

func readVersion(t *testing.T, r *componentVersionResource, model componentVersionModel) resource.ReadResponse {
	t.Helper()
	state := componentVersionState(t, r, model)
	response := resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &response)
	return response
}

func TestComponentVersionReadRefreshesRemoteFieldsAndCanonicalDefinition(t *testing.T) {
	r, fake, _ := versionHarness(t)
	remote := versionFixture("TESTING")
	remote.Description, remote.SoftwareVendor, remote.SoftwareVersion = "changed", "new vendor", "2.0"
	remote.Name, remote.CreatedBy, remote.UpdatedBy = "2.0.0", "new creator", "new updater"
	remote.CreatedAt, remote.UpdatedAt = "2026-09-21T00:00:00Z", "2026-09-23T00:00:00Z"
	license, notes, position := "dashboard", "notes", "APPEND"
	remote.LicenseDashboard, remote.Notes = &license, &notes
	remote.Definition = json.RawMessage(`{ "phases": [ { "steps": [ {} ] } ] }`)
	remote.Dependencies = []vewcomponents.Dependency{
		{ComponentID: "b", ComponentName: "B", VersionID: "vb", VersionName: "2", Type: "HELPER", Order: 2},
		{ComponentID: "a", ComponentName: "A", VersionID: "va", VersionName: "1", Type: "MAIN", Order: 1, Position: &position},
	}
	fake.QueueVersionReads("project", "component", "version", testhelpers.VersionResponse{Version: remote})
	model := validComponentVersionModel(t)
	model.Timeouts = timeoutsValue(t, "2m", "3m", "4m")
	response := readVersion(t, r, model)
	if response.Diagnostics.HasError() {
		t.Fatal(response.Diagnostics)
	}
	got := decodeVersionState(t, response.State)
	want := model
	want.Description, want.SoftwareVendor, want.SoftwareVersion = types.StringValue("changed"), types.StringValue("new vendor"), types.StringValue("2.0")
	want.Name, want.CreatedBy, want.UpdatedBy = types.StringValue("2.0.0"), types.StringValue("new creator"), types.StringValue("new updater")
	want.CreatedAt, want.UpdatedAt = types.StringValue("2026-09-21T00:00:00Z"), types.StringValue("2026-09-23T00:00:00Z")
	want.Status, want.LicenseDashboard, want.Notes = types.StringValue("TESTING"), types.StringValue("dashboard"), types.StringValue("notes")
	want.DefinitionJSON = types.StringValue(`{"phases":[{"steps":[{"maxAttempts":1,"onFailure":"Abort","timeoutSeconds":7200}]}]}`)
	want.Dependencies = dependencyList(
		dependencyModel{ComponentID: types.StringValue("a"), ComponentName: types.StringValue("A"), VersionID: types.StringValue("va"), VersionName: types.StringValue("1"), Type: types.StringValue("MAIN"), Order: types.Int64Value(1), Position: types.StringValue("APPEND")},
		dependencyModel{ComponentID: types.StringValue("b"), ComponentName: types.StringValue("B"), VersionID: types.StringValue("vb"), VersionName: types.StringValue("2"), Type: types.StringValue("HELPER"), Order: types.Int64Value(2), Position: types.StringNull()},
	)
	if !response.State.Raw.Equal(componentVersionState(t, r, want).Raw) {
		t.Fatalf("state = %#v, want %#v", got, want)
	}
	assertVersionMethods(t, fake, "GET")
}

func TestComponentVersionReadPreservesConfiguredReleaseType(t *testing.T) {
	for _, release := range []types.String{types.StringValue("PATCH"), types.StringNull()} {
		r, _, _ := versionHarness(t, "VALIDATED")
		model := validComponentVersionModel(t)
		model.ReleaseType = release
		response := readVersion(t, r, model)
		if response.Diagnostics.HasError() {
			t.Fatal(response.Diagnostics)
		}
		if got := decodeVersionState(t, response.State); !got.ReleaseType.Equal(release) {
			t.Fatalf("release = %v", got.ReleaseType)
		}
	}
}

func TestComponentVersionReadRemovesStateOnNotFound(t *testing.T) {
	r, fake, _ := versionHarness(t)
	response := readVersion(t, r, validComponentVersionModel(t))
	if response.Diagnostics.HasError() || !response.State.Raw.IsNull() {
		t.Fatalf("state/diagnostics = %v/%v", response.State.Raw, response.Diagnostics)
	}
	assertVersionMethods(t, fake, "GET")
}

func TestComponentVersionReadRemovesStateOnRetired(t *testing.T) {
	r, fake, _ := versionHarness(t, "RETIRED")
	response := readVersion(t, r, validComponentVersionModel(t))
	if response.Diagnostics.HasError() || !response.State.Raw.IsNull() {
		t.Fatalf("state/diagnostics = %v/%v", response.State.Raw, response.Diagnostics)
	}
	assertVersionMethods(t, fake, "GET")
}

func TestComponentVersionResourceSchema(t *testing.T) {
	r := NewComponentVersionResource()
	var response resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &response)

	wantRequired := []string{
		"project_id", "component_id", "description", "release_type", "definition_json", "software_vendor", "software_version",
	}
	wantOptional := []string{"dependencies", "license_dashboard", "notes"}
	wantComputed := []string{"id", "name", "status", "created_at", "created_by", "updated_at", "updated_by"}
	if len(response.Schema.Attributes) != len(wantRequired)+len(wantOptional)+len(wantComputed) {
		t.Fatalf("attributes = %d, want %d", len(response.Schema.Attributes), len(wantRequired)+len(wantOptional)+len(wantComputed))
	}
	for _, name := range wantRequired {
		attribute, ok := response.Schema.Attributes[name]
		if !ok || !attribute.IsRequired() {
			t.Fatalf("%s must be a required attribute, got %#v", name, attribute)
		}
	}
	for _, name := range wantOptional {
		attribute, ok := response.Schema.Attributes[name]
		if !ok || !attribute.IsOptional() {
			t.Fatalf("%s must be an optional attribute, got %#v", name, attribute)
		}
	}
	for _, name := range wantComputed {
		attribute, ok := response.Schema.Attributes[name]
		if !ok || !attribute.IsComputed() {
			t.Fatalf("%s must be a computed attribute, got %#v", name, attribute)
		}
	}

	for _, name := range []string{"project_id", "component_id"} {
		attribute, ok := response.Schema.Attributes[name].(resourceschema.StringAttribute)
		if !ok || len(attribute.PlanModifiers) != 1 {
			t.Fatalf("%s must have one string replacement modifier, got %#v", name, response.Schema.Attributes[name])
		}
	}
	if attribute, ok := response.Schema.Attributes["release_type"].(resourceschema.StringAttribute); !ok || len(attribute.PlanModifiers) != 0 {
		t.Fatalf("release_type must defer replacement to ModifyPlan, got %#v", response.Schema.Attributes["release_type"])
	}
	dependencies, ok := response.Schema.Attributes["dependencies"].(resourceschema.ListNestedAttribute)
	if !ok || dependencies.Default == nil || len(dependencies.NestedObject.Attributes) != 7 {
		t.Fatalf("dependencies schema = %#v, want optional list with default and seven nested attributes", response.Schema.Attributes["dependencies"])
	}
	for _, name := range []string{"component_id", "component_name", "version_id", "version_name", "order"} {
		if attribute, ok := dependencies.NestedObject.Attributes[name]; !ok || !attribute.IsRequired() {
			t.Fatalf("dependencies.%s must be required, got %#v", name, attribute)
		}
	}
	for _, name := range []string{"type", "position"} {
		if attribute, ok := dependencies.NestedObject.Attributes[name]; !ok || !attribute.IsOptional() {
			t.Fatalf("dependencies.%s must be optional, got %#v", name, attribute)
		}
	}
	timeouts, ok := response.Schema.Blocks["timeouts"].(resourceschema.SingleNestedBlock)
	if !ok || len(timeouts.Attributes) != 3 {
		t.Fatalf("timeouts schema = %#v, want one nested block with create/update/delete", response.Schema.Blocks["timeouts"])
	}
	for _, name := range []string{"create", "update", "delete"} {
		attribute, ok := timeouts.Attributes[name]
		if !ok || !attribute.IsOptional() {
			t.Fatalf("timeouts.%s must be optional, got %#v", name, attribute)
		}
	}
}

func updateVersion(t *testing.T, r *componentVersionResource, state, plan componentVersionModel) resource.UpdateResponse {
	t.Helper()
	s, p := componentVersionState(t, r, state), componentVersionState(t, r, plan)
	response := resource.UpdateResponse{State: s}
	r.Update(context.Background(), resource.UpdateRequest{State: s, Plan: tfsdk.Plan{Raw: p.Raw, Schema: p.Schema}}, &response)
	return response
}

func TestComponentVersionUpdateSendsFullMutablePayloadAndWaitsForValidated(t *testing.T) {
	r, fake, w := versionHarness(t, "VALIDATED", "UPDATING", "CREATED", "TESTING", "VALIDATED")
	state, plan := validComponentVersionModel(t), validComponentVersionModel(t)
	plan.Description, plan.SoftwareVendor, plan.SoftwareVersion = types.StringValue("changed"), types.StringValue("new vendor"), types.StringValue("2.0")
	plan.LicenseDashboard, plan.Notes = types.StringValue("dashboard"), types.StringValue("notes")
	plan.Timeouts = timeoutsValue(t, "", "3m", "")
	fake.SetVersionRetryAfter("7")
	response := updateVersion(t, r, state, plan)
	if response.Diagnostics.HasError() {
		t.Fatal(response.Diagnostics)
	}
	assertVersionMethods(t, fake, "GET", "PUT", "GET", "GET", "GET", "GET")
	var payload map[string]any
	if err := json.Unmarshal(fake.VersionRequests()[1].Body, &payload); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"componentVersionDescription": "changed", "componentVersionDefinition": map[string]any{"phases": []any{}}, "componentVersionDependencies": []any{}, "softwareVendor": "new vendor", "softwareVersion": "2.0", "licenseDashboard": "dashboard", "notes": "notes"}
	if !reflect.DeepEqual(payload, want) {
		t.Fatalf("payload = %#v, want %#v", payload, want)
	}
	got := decodeVersionState(t, response.State)
	if got.Status.ValueString() != "VALIDATED" || !got.ReleaseType.Equal(plan.ReleaseType) || !got.Timeouts.Equal(plan.Timeouts) {
		t.Fatalf("state = %#v", got)
	}
	if w.timeout != 3*time.Minute || w.delay != 7*time.Second {
		t.Fatalf("timeout/delay = %v/%v", w.timeout, w.delay)
	}
}

func TestComponentVersionUpdateAdoptsImportedReleaseTypeWithoutPut(t *testing.T) {
	for _, priorRelease := range []types.String{types.StringNull(), types.StringUnknown()} {
		r, fake, _ := versionHarness(t, "VALIDATED")
		state, plan := validComponentVersionModel(t), validComponentVersionModel(t)
		state.ReleaseType = priorRelease
		plan.ReleaseType = types.StringValue("PATCH")
		response := updateVersion(t, r, state, plan)
		if response.Diagnostics.HasError() {
			t.Fatal(response.Diagnostics)
		}
		assertVersionMethods(t, fake, "GET")
		got := decodeVersionState(t, response.State)
		if got.ReleaseType.ValueString() != "PATCH" || got.Status.ValueString() != "VALIDATED" {
			t.Fatalf("state = %#v", got)
		}
	}
}

func TestComponentVersionUpdateWaitsForPendingRemoteOperationBeforePut(t *testing.T) {
	r, fake, _ := versionHarness(t, "UPDATING", "CREATED", "TESTING", "VALIDATED", "UPDATING", "CREATED", "TESTING", "VALIDATED")
	state, plan := validComponentVersionModel(t), validComponentVersionModel(t)
	plan.Description = types.StringValue("changed")
	response := updateVersion(t, r, state, plan)
	if response.Diagnostics.HasError() {
		t.Fatal(response.Diagnostics)
	}
	assertVersionMethods(t, fake, "GET", "GET", "GET", "GET", "PUT", "GET", "GET", "GET", "GET")
}

func TestComponentVersionUpdatePreservesStateOnFailedStatus(t *testing.T) {
	r, fake, _ := versionHarness(t, "VALIDATED", "UPDATING", "FAILED")
	state, plan := validComponentVersionModel(t), validComponentVersionModel(t)
	plan.Description = types.StringValue("changed")
	response := updateVersion(t, r, state, plan)
	assertRecoverableVersion(t, response.State, response.Diagnostics, "FAILED")
	assertVersionMethods(t, fake, "GET", "PUT", "GET", "GET")
}

func TestComponentVersionUpdateRetriesExistingFailedVersion(t *testing.T) {
	r, fake, _ := versionHarness(t, "FAILED", "UPDATING", "CREATED", "TESTING", "VALIDATED")
	state, plan := validComponentVersionModel(t), validComponentVersionModel(t)
	state.Status, plan.Description = types.StringValue("FAILED"), types.StringValue("retry")
	response := updateVersion(t, r, state, plan)
	if response.Diagnostics.HasError() {
		t.Fatal(response.Diagnostics)
	}
	assertVersionMethods(t, fake, "GET", "PUT", "GET", "GET", "GET", "GET")
}

func TestComponentVersionUpdateAdoptionWithMutableChangeStillPuts(t *testing.T) {
	r, fake, _ := versionHarness(t, "VALIDATED", "VALIDATED")
	state, plan := validComponentVersionModel(t), validComponentVersionModel(t)
	state.ReleaseType, plan.Description = types.StringNull(), types.StringValue("changed")
	response := updateVersion(t, r, state, plan)
	if response.Diagnostics.HasError() {
		t.Fatal(response.Diagnostics)
	}
	assertVersionMethods(t, fake, "GET", "PUT", "GET")
}

func deleteVersion(t *testing.T, r *componentVersionResource, model componentVersionModel) resource.DeleteResponse {
	t.Helper()
	s := componentVersionState(t, r, model)
	response := resource.DeleteResponse{State: s}
	r.Delete(context.Background(), resource.DeleteRequest{State: s}, &response)
	return response
}

func TestComponentVersionDeleteWaitsForPendingOperationThenRetires(t *testing.T) {
	for _, settled := range []string{"VALIDATED", "FAILED", "RELEASED"} {
		t.Run(settled, func(t *testing.T) {
			r, fake, _ := versionHarness(t, "UPDATING", "CREATED", "TESTING", settled, "UPDATING", "RETIRED")
			response := deleteVersion(t, r, validComponentVersionModel(t))
			if response.Diagnostics.HasError() || !response.State.Raw.IsNull() {
				t.Fatalf("delete: %v, state null = %v", response.Diagnostics, response.State.Raw.IsNull())
			}
			assertVersionMethods(t, fake, "GET", "GET", "GET", "GET", "DELETE", "GET", "GET")
		})
	}
}

func TestComponentVersionDeleteWaitsUntilRetired(t *testing.T) {
	for _, status := range []string{"VALIDATED", "FAILED", "RELEASED"} {
		t.Run(status, func(t *testing.T) {
			r, fake, w := versionHarness(t, status, "UPDATING", "RETIRED")
			fake.SetVersionRetryAfter("5")
			model := validComponentVersionModel(t)
			model.Timeouts = timeoutsValue(t, "", "", "4m")
			response := deleteVersion(t, r, model)
			if response.Diagnostics.HasError() || !response.State.Raw.IsNull() {
				t.Fatalf("delete: %v, state null = %v", response.Diagnostics, response.State.Raw.IsNull())
			}
			assertVersionMethods(t, fake, "GET", "DELETE", "GET", "GET")
			if w.timeout != 4*time.Minute || w.delay != 5*time.Second {
				t.Fatalf("timeout/delay = %v/%v", w.timeout, w.delay)
			}
		})
	}
}

func TestComponentVersionDeleteTreatsNotFoundAsSuccess(t *testing.T) {
	for _, stage := range []string{"initial", "pending", "retiring", "already retired"} {
		t.Run(stage, func(t *testing.T) {
			r, fake, _ := versionHarness(t)
			want := []string{"GET"}
			switch stage {
			case "pending":
				fake.QueueVersionReads("project", "component", "version", testhelpers.VersionResponse{Version: versionFixture("UPDATING")}, testhelpers.VersionResponse{StatusCode: 404})
				want = []string{"GET", "GET"}
			case "retiring":
				fake.QueueVersionReads("project", "component", "version", testhelpers.VersionResponse{Version: versionFixture("VALIDATED")}, testhelpers.VersionResponse{StatusCode: 404})
				want = []string{"GET", "DELETE", "GET"}
			case "already retired":
				fake.QueueVersionReads("project", "component", "version", testhelpers.VersionResponse{Version: versionFixture("RETIRED")})
			}
			response := deleteVersion(t, r, validComponentVersionModel(t))
			if response.Diagnostics.HasError() || !response.State.Raw.IsNull() {
				t.Fatalf("delete: %v, state null = %v", response.Diagnostics, response.State.Raw.IsNull())
			}
			assertVersionMethods(t, fake, want...)
		})
	}
}

func TestComponentVersionDeletePreservesStateOnFailedStatus(t *testing.T) {
	r, fake, _ := versionHarness(t, "VALIDATED", "UPDATING", "FAILED")
	response := deleteVersion(t, r, validComponentVersionModel(t))
	assertRecoverableVersion(t, response.State, response.Diagnostics, "FAILED")
	assertVersionMethods(t, fake, "GET", "DELETE", "GET", "GET")
}

func TestComponentVersionDeletePreservesStateOnTimeout(t *testing.T) {
	r, _, w := versionHarness(t, "VALIDATED", "UPDATING")
	w.limit = 1
	response := deleteVersion(t, r, validComponentVersionModel(t))
	assertRecoverableVersion(t, response.State, response.Diagnostics, "UPDATING")
}

func TestComponentVersionResourceConfigure(t *testing.T) {
	r := NewComponentVersionResource().(*componentVersionResource)
	var response resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: "wrong"}, &response)
	if !response.Diagnostics.HasError() || !strings.Contains(response.Diagnostics[0].Detail(), "providerdata.Data") {
		t.Fatalf("wrong-type diagnostics = %v", response.Diagnostics)
	}

	response.Diagnostics = nil
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: providerdata.Data{}}, &response)
	if !response.Diagnostics.HasError() || !strings.Contains(response.Diagnostics[0].Detail(), "ComponentVersions") {
		t.Fatalf("missing-component-version API diagnostics = %v", response.Diagnostics)
	}

	response.Diagnostics = nil
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: providerdata.Data{ComponentVersions: componentVersionAPIStub{}, Waiter: waiterStub{}}}, &response)
	if response.Diagnostics.HasError() || r.client == nil || r.waiter == nil {
		t.Fatalf("resource was not configured: client=%T waiter=%T diagnostics=%v", r.client, r.waiter, response.Diagnostics)
	}
}

func TestComponentVersionResourceValidateConfigRejectsKnownInvalidValues(t *testing.T) {
	r := NewComponentVersionResource().(*componentVersionResource)
	for _, test := range []struct {
		name     string
		mutate   func(*componentVersionModel)
		wantPath string
	}{
		{"release type", func(model *componentVersionModel) { model.ReleaseType = types.StringValue("BUILD") }, "release_type"},
		{"definition JSON", func(model *componentVersionModel) { model.DefinitionJSON = types.StringValue("{") }, "definition_json"},
		{"duplicate dependency order", func(model *componentVersionModel) {
			model.Dependencies = dependencyList(dependencyModelWithOrder(1), dependencyModelWithOrder(1))
		}, "dependency"},
		{"negative dependency order", func(model *componentVersionModel) { model.Dependencies = dependencyList(dependencyModelWithOrder(-1)) }, "dependency"},
		{"invalid dependency type", func(model *componentVersionModel) {
			dependency := dependencyModelWithOrder(1)
			dependency.Type = types.StringValue("RUNTIME")
			model.Dependencies = dependencyList(dependency)
		}, "dependency"},
		{"invalid dependency position", func(model *componentVersionModel) {
			dependency := dependencyModelWithOrder(1)
			dependency.Position = types.StringValue("BEFORE")
			model.Dependencies = dependencyList(dependency)
		}, "dependency"},
		{"malformed timeout", func(model *componentVersionModel) { model.Timeouts = timeoutsValue(t, "never", "", "") }, "timeouts"},
		{"non-positive timeout", func(model *componentVersionModel) { model.Timeouts = timeoutsValue(t, "0s", "", "") }, "timeouts"},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			model := validComponentVersionModel(t)
			test.mutate(&model)
			var response resource.ValidateConfigResponse
			r.ValidateConfig(context.Background(), resource.ValidateConfigRequest{Config: componentVersionConfig(t, r, model)}, &response)
			if !response.Diagnostics.HasError() || !diagnosticsContain(response.Diagnostics, test.wantPath) {
				t.Fatalf("diagnostics = %v, want error about %s", response.Diagnostics, test.wantPath)
			}
		})
	}
}

func TestComponentVersionResourceValidateConfigDoesNotCallAPI(t *testing.T) {
	r := NewComponentVersionResource().(*componentVersionResource)
	api := &componentVersionAPICounter{}
	r.client = api
	var response resource.ValidateConfigResponse
	r.ValidateConfig(context.Background(), resource.ValidateConfigRequest{Config: componentVersionConfig(t, r, validComponentVersionModel(t))}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("validation diagnostics = %v", response.Diagnostics)
	}
	if api.calls != 0 {
		t.Fatalf("validation API calls = %d, want 0", api.calls)
	}
}

func TestComponentVersionResourceValidateConfigDefersUnknownValues(t *testing.T) {
	r := NewComponentVersionResource().(*componentVersionResource)
	model := validComponentVersionModel(t)
	model.ReleaseType = types.StringUnknown()
	model.DefinitionJSON = types.StringUnknown()
	model.Dependencies = types.ListUnknown(types.ObjectType{AttrTypes: dependencyAttributeTypes})
	model.Timeouts = types.ObjectUnknown(timeoutAttributeTypes)
	var response resource.ValidateConfigResponse
	r.ValidateConfig(context.Background(), resource.ValidateConfigRequest{Config: componentVersionConfig(t, r, model)}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("unknown values should defer validation, diagnostics = %v", response.Diagnostics)
	}
}

func TestComponentVersionResourceValidateConfigDefersUnknownNestedDependencyValues(t *testing.T) {
	r := NewComponentVersionResource().(*componentVersionResource)
	for _, test := range []struct {
		name   string
		mutate func(*dependencyModel)
	}{
		{"order", func(dependency *dependencyModel) { dependency.Order = types.Int64Unknown() }},
		{"type", func(dependency *dependencyModel) { dependency.Type = types.StringUnknown() }},
		{"position", func(dependency *dependencyModel) { dependency.Position = types.StringUnknown() }},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			model := validComponentVersionModel(t)
			dependency := dependencyModelWithOrder(1)
			test.mutate(&dependency)
			model.Dependencies = dependencyList(dependency)
			var response resource.ValidateConfigResponse
			r.ValidateConfig(context.Background(), resource.ValidateConfigRequest{Config: componentVersionConfig(t, r, model)}, &response)
			if response.Diagnostics.HasError() {
				t.Fatalf("unknown dependency.%s should defer validation, diagnostics = %v", test.name, response.Diagnostics)
			}
		})
	}
}

func TestComponentVersionResourceValidateConfigRejectsKnownInvalidDependencyAlongsideDeferredDependency(t *testing.T) {
	r := NewComponentVersionResource().(*componentVersionResource)
	model := validComponentVersionModel(t)
	deferred := dependencyModelWithOrder(1)
	deferred.Order = types.Int64Unknown()
	invalid := dependencyModelWithOrder(2)
	invalid.Type = types.StringValue("RUNTIME")
	model.Dependencies = dependencyList(deferred, invalid)
	var response resource.ValidateConfigResponse
	r.ValidateConfig(context.Background(), resource.ValidateConfigRequest{Config: componentVersionConfig(t, r, model)}, &response)
	if !response.Diagnostics.HasError() || !diagnosticsContain(response.Diagnostics, "dependency[1].type") {
		t.Fatalf("diagnostics = %v, want invalid known dependency[1].type", response.Diagnostics)
	}
}

func TestOperationTimeoutUsesDefaultsAndConfiguredValues(t *testing.T) {
	timeouts := types.ObjectNull(timeoutAttributeTypes)
	for _, test := range []struct {
		operation string
		want      time.Duration
	}{
		{"create", time.Hour},
		{"update", time.Hour},
		{"delete", 30 * time.Minute},
	} {
		if got := operationTimeout(context.Background(), timeouts, test.operation); got != test.want {
			t.Fatalf("default %s timeout = %s, want %s", test.operation, got, test.want)
		}
	}
	timeouts = timeoutsValue(t, "3m", "4m", "5m")
	for _, test := range []struct {
		operation string
		want      time.Duration
	}{
		{"create", 3 * time.Minute},
		{"update", 4 * time.Minute},
		{"delete", 5 * time.Minute},
	} {
		if got := operationTimeout(context.Background(), timeouts, test.operation); got != test.want {
			t.Fatalf("configured %s timeout = %s, want %s", test.operation, got, test.want)
		}
	}
}

func TestComponentVersionImportParsesThreePartID(t *testing.T) {
	projectID, componentID, versionID, err := parseComponentVersionImportID("project/component/version")
	if err != nil || projectID != "project" || componentID != "component" || versionID != "version" {
		t.Fatalf("parsed import = %q/%q/%q, %v", projectID, componentID, versionID, err)
	}
}

func TestComponentVersionImportRejectsWrongPartCount(t *testing.T) {
	for _, value := range []string{"", "project/component", "project/component/version/extra"} {
		if _, _, _, err := parseComponentVersionImportID(value); err == nil {
			t.Fatalf("import ID %q was accepted", value)
		}
	}
}

func TestComponentVersionImportRejectsEmptyPart(t *testing.T) {
	for _, value := range []string{"/component/version", "project//version", "project/component/"} {
		if _, _, _, err := parseComponentVersionImportID(value); err == nil {
			t.Fatalf("import ID %q was accepted", value)
		}
	}
}

func TestComponentVersionImportSetsIdentityAndLeavesReleaseTypeNull(t *testing.T) {
	r := NewComponentVersionResource().(*componentVersionResource)
	model := validComponentVersionModel(t)
	model.ReleaseType = types.StringValue("MAJOR")
	state := componentVersionState(t, r, model)
	var response resource.ImportStateResponse
	response.State = state
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: "project/component/version"}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("import diagnostics = %v", response.Diagnostics)
	}
	var got componentVersionModel
	if diagnostics := response.State.Get(context.Background(), &got); diagnostics.HasError() {
		t.Fatalf("decode state diagnostics = %v", diagnostics)
	}
	if got.ProjectID.ValueString() != "project" || got.ComponentID.ValueString() != "component" || got.ID.ValueString() != "version" || !got.ReleaseType.IsNull() {
		t.Fatalf("imported state = %#v", got)
	}
}

func TestImportedComponentVersionAdoptsReleaseTypeWithoutReplacement(t *testing.T) {
	r := NewComponentVersionResource().(*componentVersionResource)
	for _, priorReleaseType := range []types.String{types.StringNull(), types.StringUnknown()} {
		state := validComponentVersionModel(t)
		state.ReleaseType = priorReleaseType
		plan := state
		plan.ReleaseType = types.StringValue("PATCH")
		response := modifyPlan(t, r, state, plan)
		if response.Diagnostics.HasError() || len(response.RequiresReplace) != 0 {
			t.Fatalf("adoption diagnostics/replacements = %v/%v", response.Diagnostics, response.RequiresReplace)
		}
	}
}

func TestKnownReleaseTypeChangeRequiresReplacement(t *testing.T) {
	r := NewComponentVersionResource().(*componentVersionResource)
	state := validComponentVersionModel(t)
	state.ReleaseType = types.StringValue("MAJOR")
	plan := state
	plan.ReleaseType = types.StringValue("PATCH")
	response := modifyPlan(t, r, state, plan)
	if response.Diagnostics.HasError() || !containsPath(response.RequiresReplace, path.Root("release_type")) {
		t.Fatalf("release type change diagnostics/replacements = %v/%v", response.Diagnostics, response.RequiresReplace)
	}
}

func TestReleasedComponentVersionMutableChangesRequireReplacement(t *testing.T) {
	r := NewComponentVersionResource().(*componentVersionResource)
	for _, test := range []struct {
		name   string
		path   path.Path
		mutate func(*componentVersionModel)
	}{
		{"description", path.Root("description"), func(model *componentVersionModel) { model.Description = types.StringValue("changed") }},
		{"definition", path.Root("definition_json"), func(model *componentVersionModel) { model.DefinitionJSON = types.StringValue(`{"changed":true}`) }},
		{"dependencies", path.Root("dependencies"), func(model *componentVersionModel) { model.Dependencies = dependencyList(dependencyModelWithOrder(2)) }},
		{"software vendor", path.Root("software_vendor"), func(model *componentVersionModel) { model.SoftwareVendor = types.StringValue("changed") }},
		{"software version", path.Root("software_version"), func(model *componentVersionModel) { model.SoftwareVersion = types.StringValue("changed") }},
		{"license dashboard", path.Root("license_dashboard"), func(model *componentVersionModel) { model.LicenseDashboard = types.StringValue("changed") }},
		{"notes", path.Root("notes"), func(model *componentVersionModel) { model.Notes = types.StringValue("changed") }},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			state := validComponentVersionModel(t)
			state.Status = types.StringValue("RELEASED")
			plan := state
			test.mutate(&plan)
			response := modifyPlan(t, r, state, plan)
			if response.Diagnostics.HasError() || !containsPath(response.RequiresReplace, test.path) {
				t.Fatalf("released change diagnostics/replacements = %v/%v", response.Diagnostics, response.RequiresReplace)
			}
		})
	}
}

func TestReleasedComponentVersionUnknownValuesDoNotRequireReplacement(t *testing.T) {
	r := NewComponentVersionResource().(*componentVersionResource)
	state := validComponentVersionModel(t)
	state.Status = types.StringValue("RELEASED")
	plan := state
	plan.Description = types.StringUnknown()
	response := modifyPlan(t, r, state, plan)
	if response.Diagnostics.HasError() || len(response.RequiresReplace) != 0 {
		t.Fatalf("unknown plan diagnostics/replacements = %v/%v", response.Diagnostics, response.RequiresReplace)
	}
}

type componentVersionAPIStub struct{}

func (componentVersionAPIStub) CreateComponentVersion(context.Context, string, string, vewcomponents.CreateComponentVersionInput) (vewcomponents.ActionResult, error) {
	return vewcomponents.ActionResult{}, nil
}
func (componentVersionAPIStub) GetComponentVersion(context.Context, string, string, string) (vewcomponents.ComponentVersion, error) {
	return vewcomponents.ComponentVersion{}, nil
}
func (componentVersionAPIStub) UpdateComponentVersion(context.Context, string, string, string, vewcomponents.UpdateComponentVersionInput) (vewcomponents.ActionResult, error) {
	return vewcomponents.ActionResult{}, nil
}
func (componentVersionAPIStub) RetireComponentVersion(context.Context, string, string, string) (vewcomponents.ActionResult, error) {
	return vewcomponents.ActionResult{}, nil
}

type componentVersionAPICounter struct {
	calls int
}

func (api *componentVersionAPICounter) CreateComponentVersion(context.Context, string, string, vewcomponents.CreateComponentVersionInput) (vewcomponents.ActionResult, error) {
	api.calls++
	return vewcomponents.ActionResult{}, nil
}
func (api *componentVersionAPICounter) GetComponentVersion(context.Context, string, string, string) (vewcomponents.ComponentVersion, error) {
	api.calls++
	return vewcomponents.ComponentVersion{}, nil
}
func (api *componentVersionAPICounter) UpdateComponentVersion(context.Context, string, string, string, vewcomponents.UpdateComponentVersionInput) (vewcomponents.ActionResult, error) {
	api.calls++
	return vewcomponents.ActionResult{}, nil
}
func (api *componentVersionAPICounter) RetireComponentVersion(context.Context, string, string, string) (vewcomponents.ActionResult, error) {
	api.calls++
	return vewcomponents.ActionResult{}, nil
}

type waiterStub struct{}

func (waiterStub) Until(context.Context, time.Duration, time.Duration, vew.StatusReader, vew.StatusEvaluator) error {
	return nil
}

func validComponentVersionModel(t *testing.T) componentVersionModel {
	t.Helper()
	return componentVersionModel{
		ID:               types.StringValue("version"),
		ProjectID:        types.StringValue("project"),
		ComponentID:      types.StringValue("component"),
		Description:      types.StringValue("description"),
		ReleaseType:      types.StringValue("MAJOR"),
		DefinitionJSON:   types.StringValue(`{"phases":[]}`),
		Dependencies:     types.ListValueMust(types.ObjectType{AttrTypes: dependencyAttributeTypes}, nil),
		SoftwareVendor:   types.StringValue("vendor"),
		SoftwareVersion:  types.StringValue("1.0"),
		LicenseDashboard: types.StringNull(),
		Notes:            types.StringNull(),
		Name:             types.StringValue("1.0.0"),
		Status:           types.StringValue("VALIDATED"),
		CreatedAt:        types.StringValue("2026-09-22T00:00:00Z"),
		CreatedBy:        types.StringValue("creator"),
		UpdatedAt:        types.StringValue("2026-09-22T00:00:00Z"),
		UpdatedBy:        types.StringValue("updater"),
		Timeouts:         types.ObjectNull(timeoutAttributeTypes),
	}
}

func timeoutsValue(t *testing.T, create, update, delete string) types.Object {
	t.Helper()
	value := func(raw string) types.String {
		if raw == "" {
			return types.StringNull()
		}
		return types.StringValue(raw)
	}
	return types.ObjectValueMust(timeoutAttributeTypes, map[string]attr.Value{
		"create": value(create),
		"update": value(update),
		"delete": value(delete),
	})
}

func componentVersionConfig(t *testing.T, r resource.Resource, model componentVersionModel) tfsdk.Config {
	t.Helper()
	state := componentVersionState(t, r, model)
	return tfsdk.Config{Raw: state.Raw, Schema: state.Schema}
}

func componentVersionState(t *testing.T, r resource.Resource, model componentVersionModel) tfsdk.State {
	t.Helper()
	var schemaResponse resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &schemaResponse)
	state := tfsdk.State{Schema: schemaResponse.Schema}
	if diagnostics := state.Set(context.Background(), &model); diagnostics.HasError() {
		t.Fatalf("encode state diagnostics = %v", diagnostics)
	}
	return state
}

func modifyPlan(t *testing.T, r *componentVersionResource, state, plan componentVersionModel) resource.ModifyPlanResponse {
	t.Helper()
	terraformState := componentVersionState(t, r, state)
	terraformPlan := componentVersionState(t, r, plan)
	var response resource.ModifyPlanResponse
	response.Plan = tfsdk.Plan{Raw: terraformPlan.Raw, Schema: terraformPlan.Schema}
	r.ModifyPlan(context.Background(), resource.ModifyPlanRequest{State: terraformState, Plan: response.Plan}, &response)
	return response
}

func containsPath(paths path.Paths, want path.Path) bool {
	for _, candidate := range paths {
		if candidate.Equal(want) {
			return true
		}
	}
	return false
}

func diagnosticsContain(diagnostics diag.Diagnostics, want string) bool {
	for _, diagnostic := range diagnostics {
		if strings.Contains(diagnostic.Summary(), want) || strings.Contains(diagnostic.Detail(), want) {
			return true
		}
	}
	return false
}
