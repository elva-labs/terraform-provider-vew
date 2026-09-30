package recipes

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	vewrecipes "github.com/elva-labs/terraform-provider-vew/internal/vew/recipes"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

type recipeAPIStub struct {
	createInput vewrecipes.CreateRecipeInput
	created     int
	archived    int
	archiveErr  error
	readErr     error
	remote      vewrecipes.Recipe
}

func (f *recipeAPIStub) CreateRecipe(_ context.Context, _ string, input vewrecipes.CreateRecipeInput) (string, error) {
	f.created++
	f.createInput = input
	return "recipe", nil
}
func (f *recipeAPIStub) GetRecipe(context.Context, string, string) (vewrecipes.Recipe, error) {
	return f.remote, f.readErr
}
func (f *recipeAPIStub) ArchiveRecipe(context.Context, string, string) error {
	f.archived++
	return f.archiveErr
}

func recipeFixture() vewrecipes.Recipe {
	return vewrecipes.Recipe{ID: "recipe", Name: "recipe name", Description: "remote description", Platform: "Linux", Architecture: "amd64", OSVersion: "Ubuntu 24", Status: "CREATED", CreatedAt: "2026-09-23T00:00:00Z", CreatedBy: "creator", UpdatedAt: "2026-09-23T00:00:00Z", UpdatedBy: "updater"}
}

func recipePlan() recipeModel {
	return recipeModel{ID: types.StringNull(), ProjectID: types.StringValue("project"), Name: types.StringValue("recipe name"), Description: types.StringValue("submitted description"), Platform: types.StringValue("Linux"), Architecture: types.StringValue("amd64"), OSVersion: types.StringValue("Ubuntu 24"), Status: types.StringNull(), CreatedAt: types.StringNull(), CreatedBy: types.StringNull(), UpdatedAt: types.StringNull(), UpdatedBy: types.StringNull()}
}

func recipeState(t *testing.T, model recipeModel) tfsdk.State {
	t.Helper()
	var schemaResponse resource.SchemaResponse
	NewRecipeResource().Schema(context.Background(), resource.SchemaRequest{}, &schemaResponse)
	state := tfsdk.State{Schema: schemaResponse.Schema}
	if d := state.Set(context.Background(), &model); d.HasError() {
		t.Fatalf("encode recipe state: %v", d)
	}
	return state
}

func TestRecipeResourceCreateReadArchive(t *testing.T) {
	client := &recipeAPIStub{remote: recipeFixture()}
	r := &recipeResource{client: client}
	plan := recipeState(t, recipePlan())
	response := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema}}
	r.Create(context.Background(), resource.CreateRequest{Plan: tfsdk.Plan{Schema: plan.Schema, Raw: plan.Raw}}, &response)
	if response.Diagnostics.HasError() || client.created != 1 {
		t.Fatalf("create diagnostics/calls: %v/%d", response.Diagnostics, client.created)
	}
	if client.createInput.Description != "submitted description" || client.createInput.OSVersion != "Ubuntu 24" {
		t.Fatalf("create input = %#v", client.createInput)
	}
	var created recipeModel
	if d := response.State.Get(context.Background(), &created); d.HasError() || created.ID.ValueString() != "recipe" || created.Description.ValueString() != "remote description" || created.Status.ValueString() != "CREATED" {
		t.Fatalf("canonical state/diagnostics = %#v/%v", created, d)
	}

	client.remote.Description = "refreshed"
	read := resource.ReadResponse{State: response.State}
	r.Read(context.Background(), resource.ReadRequest{State: response.State}, &read)
	if read.Diagnostics.HasError() {
		t.Fatalf("read diagnostics = %v", read.Diagnostics)
	}
	var refreshed recipeModel
	if d := read.State.Get(context.Background(), &refreshed); d.HasError() || refreshed.Description.ValueString() != "refreshed" {
		t.Fatalf("refreshed state/diagnostics = %#v/%v", refreshed, d)
	}

	deleted := resource.DeleteResponse{State: read.State}
	r.Delete(context.Background(), resource.DeleteRequest{State: read.State}, &deleted)
	if deleted.Diagnostics.HasError() || client.archived != 1 {
		t.Fatalf("archive diagnostics/calls = %v/%d", deleted.Diagnostics, client.archived)
	}
}

func TestRecipeResourceReadRemovesAbsent(t *testing.T) {
	for _, tc := range []struct {
		name   string
		remote vewrecipes.Recipe
		err    error
	}{
		{"404", recipeFixture(), &vew.APIError{Status: 404}},
		{"archived", func() vewrecipes.Recipe { r := recipeFixture(); r.Status = "ARCHIVED"; return r }(), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &recipeResource{client: &recipeAPIStub{remote: tc.remote, readErr: tc.err}}
			model := recipePlan()
			model.ID = types.StringValue("recipe")
			state := recipeState(t, model)
			response := resource.ReadResponse{State: state}
			r.Read(context.Background(), resource.ReadRequest{State: state}, &response)
			if response.Diagnostics.HasError() || !response.State.Raw.IsNull() {
				t.Fatalf("read should remove state: diagnostics=%v state=%v", response.Diagnostics, response.State.Raw)
			}
		})
	}
}

func TestRecipeResourceArchiveRejectionRetainsState(t *testing.T) {
	client := &recipeAPIStub{archiveErr: &vew.APIError{Status: 409, Problem: vew.Problem{Code: "ACTIVE_VERSION", Detail: "secret payload"}}}
	r := &recipeResource{client: client}
	model := recipePlan()
	model.ID = types.StringValue("recipe")
	state := recipeState(t, model)
	response := resource.DeleteResponse{State: state}
	r.Delete(context.Background(), resource.DeleteRequest{State: state}, &response)
	if !response.Diagnostics.HasError() || response.State.Raw.IsNull() {
		t.Fatalf("archive rejection should retain state: %v", response.Diagnostics)
	}
	if !strings.Contains(response.Diagnostics[0].Detail(), "ACTIVE_VERSION") || strings.Contains(response.Diagnostics[0].Detail(), "secret payload") {
		t.Fatalf("unsafe or missing diagnostic: %v", response.Diagnostics)
	}
}

func TestRecipeResourceCreateTransportError(t *testing.T) {
	client := &recipeAPIStub{readErr: errors.New("read unavailable")}
	r := &recipeResource{client: client}
	plan := recipeState(t, recipePlan())
	response := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema}}
	r.Create(context.Background(), resource.CreateRequest{Plan: tfsdk.Plan{Schema: plan.Schema, Raw: plan.Raw}}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("provisional state should be recoverable: %v", response.Diagnostics)
	}
	var model recipeModel
	if d := response.State.Get(context.Background(), &model); d.HasError() || model.ID.ValueString() != "recipe" {
		t.Fatalf("provisional identity/diagnostics = %#v/%v", model, d)
	}
}

func TestRecipeImport(t *testing.T) {
	for _, value := range []string{"", "project", "/recipe", "project/", "project/recipe/extra"} {
		if _, _, err := parseRecipeImportID(value); err == nil {
			t.Fatalf("invalid import ID %q accepted", value)
		}
	}
	client := &recipeAPIStub{remote: recipeFixture()}
	r := &recipeResource{client: client}
	model := recipePlan()
	model.ProjectID = types.StringNull()
	model.ID = types.StringNull()
	state := recipeState(t, model)
	response := resource.ImportStateResponse{State: state}
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: "project/recipe"}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("import diagnostics = %v", response.Diagnostics)
	}
	var imported recipeModel
	if d := response.State.Get(context.Background(), &imported); d.HasError() || imported.ProjectID.ValueString() != "project" || imported.ID.ValueString() != "recipe" {
		t.Fatalf("imported identity/diagnostics = %#v/%v", imported, d)
	}
	read := resource.ReadResponse{State: response.State}
	r.Read(context.Background(), resource.ReadRequest{State: response.State}, &read)
	if read.Diagnostics.HasError() {
		t.Fatalf("imported refresh diagnostics = %v", read.Diagnostics)
	}
	var refreshed recipeModel
	if d := read.State.Get(context.Background(), &refreshed); d.HasError() || refreshed.Name.ValueString() != "recipe name" || refreshed.CreatedBy.ValueString() != "creator" {
		t.Fatalf("imported refresh state/diagnostics = %#v/%v", refreshed, d)
	}
}

func TestRecipeResourceSchema(t *testing.T) {
	var response resource.SchemaResponse
	NewRecipeResource().Schema(context.Background(), resource.SchemaRequest{}, &response)
	if len(response.Schema.Attributes) != 12 {
		t.Fatalf("attributes = %d, want 12", len(response.Schema.Attributes))
	}
	for _, name := range []string{"project_id", "name", "description", "platform", "architecture", "os_version"} {
		attribute, ok := response.Schema.Attributes[name].(schema.StringAttribute)
		if !ok || !attribute.IsRequired() || len(attribute.PlanModifiers) != 1 {
			t.Fatalf("%s must be a required replacement string: %#v", name, response.Schema.Attributes[name])
		}
	}
	for _, name := range []string{"id", "status", "created_at", "created_by", "updated_at", "updated_by"} {
		attribute, ok := response.Schema.Attributes[name].(schema.StringAttribute)
		if !ok || !attribute.IsComputed() || attribute.IsRequired() || attribute.IsOptional() {
			t.Fatalf("%s must be a computed string: %#v", name, response.Schema.Attributes[name])
		}
	}
}

func TestRecipeResourcePlanReplacesCreateFields(t *testing.T) {
	var response resource.SchemaResponse
	NewRecipeResource().Schema(context.Background(), resource.SchemaRequest{}, &response)
	for _, name := range []string{"project_id", "name", "description", "platform", "architecture", "os_version"} {
		attribute := response.Schema.Attributes[name].(schema.StringAttribute)
		var planResponse planmodifier.StringResponse
		attribute.PlanModifiers[0].PlanModifyString(context.Background(), planmodifier.StringRequest{
			State:      tfsdk.State{Raw: tftypes.NewValue(tftypes.String, "prior")},
			Plan:       tfsdk.Plan{Raw: tftypes.NewValue(tftypes.String, "planned")},
			StateValue: types.StringValue("prior"),
			PlanValue:  types.StringValue("planned"),
		}, &planResponse)
		if !planResponse.RequiresReplace {
			t.Fatalf("%s change should require replacement", name)
		}
	}
}

func TestRecipeResourceValidate(t *testing.T) {
	cases := []struct {
		name, platform, architecture, osVersion, description, want string
	}{
		{"ubuntu amd64", "Linux", "amd64", "Ubuntu 24", "valid", ""},
		{"ubuntu arm64", "Linux", "arm64", "Ubuntu 24", "valid", ""},
		{"windows amd64", "Windows", "amd64", "Microsoft Windows Server 2025", "valid", ""},
		{"windows arm64", "Windows", "arm64", "Microsoft Windows Server 2025", "valid", "Unsupported recipe system"},
		{"deployment os entry", "Linux", "arm64", "Custom Ubuntu 24.04 base", "valid", ""},
		{"linux unknown architecture", "Linux", "riscv64", "Ubuntu 24", "valid", "Unsupported recipe system"},
		{"unknown platform", "macOS", "arm64", "Ubuntu 24", "valid", "Unsupported recipe system"},
		{"blank os version", "Linux", "amd64", " ", "valid", "os_version must be non-empty"},
		{"blank description", "Linux", "amd64", "Ubuntu 24", " ", "description must be non-empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var schemaResponse resource.SchemaResponse
			r := &recipeResource{}
			r.Schema(context.Background(), resource.SchemaRequest{}, &schemaResponse)
			values := map[string]tftypes.Value{}
			for name := range schemaResponse.Schema.Attributes {
				values[name] = tftypes.NewValue(tftypes.String, nil)
			}
			for name, value := range map[string]string{
				"project_id": "project", "name": "recipe", "description": tc.description,
				"platform": tc.platform, "architecture": tc.architecture, "os_version": tc.osVersion,
			} {
				values[name] = tftypes.NewValue(tftypes.String, value)
			}
			request := resource.ValidateConfigRequest{Config: tfsdk.Config{Schema: schemaResponse.Schema,
				Raw: tftypes.NewValue(schemaResponse.Schema.Type().TerraformType(context.Background()), values)}}
			var response resource.ValidateConfigResponse
			r.ValidateConfig(context.Background(), request, &response)
			if tc.want == "" && response.Diagnostics.HasError() {
				t.Fatalf("unexpected diagnostics: %v", response.Diagnostics)
			}
			if tc.want != "" {
				found := false
				for _, diagnostic := range response.Diagnostics {
					found = found || strings.Contains(diagnostic.Summary(), tc.want) || strings.Contains(diagnostic.Detail(), tc.want)
				}
				if !found {
					t.Fatalf("expected %q in diagnostics: %v", tc.want, response.Diagnostics)
				}
			}
		})
	}
}
