package components

import (
	"context"
	"strings"
	"testing"
	"time"

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

func TestComponentVersionResourceMetadata(t *testing.T) {
	r := NewComponentVersionResource()
	var response resource.MetadataResponse
	r.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "vew"}, &response)
	if response.TypeName != "vew_component_version" {
		t.Fatalf("type name = %q, want %q", response.TypeName, "vew_component_version")
	}
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
