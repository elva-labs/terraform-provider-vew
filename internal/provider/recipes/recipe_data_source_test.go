package recipes

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	vewrecipes "github.com/elva-labs/terraform-provider-vew/internal/vew/recipes"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

type recipeReadDataSourceStub struct {
	remote vewrecipes.Recipe
	err    error
	calls  int
}

func (s *recipeReadDataSourceStub) GetRecipe(context.Context, string, string) (vewrecipes.Recipe, error) {
	s.calls++
	return s.remote, s.err
}

type recipeVersionReadDataSourceStub struct {
	remote vewrecipes.RecipeVersion
	err    error
	calls  int
}

func (s *recipeVersionReadDataSourceStub) GetRecipeVersion(context.Context, string, string, string) (vewrecipes.RecipeVersion, error) {
	s.calls++
	return s.remote, s.err
}

func TestRecipeDataSourceSchema(t *testing.T) {
	var response datasource.SchemaResponse
	NewRecipeDataSource().Schema(context.Background(), datasource.SchemaRequest{}, &response)
	if _, exists := response.Schema.Attributes["id"]; exists {
		t.Fatal("data source must use recipe_id rather than a generic id")
	}
	for _, name := range []string{"project_id", "recipe_id"} {
		attribute, ok := response.Schema.Attributes[name].(schema.StringAttribute)
		if !ok || !attribute.IsRequired() || attribute.IsComputed() {
			t.Fatalf("%s must be a required selector", name)
		}
	}
	for _, name := range []string{"name", "description", "platform", "architecture", "os_version", "status", "created_at", "created_by", "updated_at", "updated_by"} {
		if !response.Schema.Attributes[name].IsComputed() {
			t.Fatalf("%s must be computed", name)
		}
	}
}

func TestRecipeVersionDataSourceSchema(t *testing.T) {
	var response datasource.SchemaResponse
	NewRecipeVersionDataSource().Schema(context.Background(), datasource.SchemaRequest{}, &response)
	if _, exists := response.Schema.Attributes["id"]; exists {
		t.Fatal("data source must use version_id rather than a generic id")
	}
	for _, name := range []string{"project_id", "recipe_id", "version_id"} {
		attribute, ok := response.Schema.Attributes[name].(schema.StringAttribute)
		if !ok || !attribute.IsRequired() || attribute.IsComputed() {
			t.Fatalf("%s must be a required selector", name)
		}
	}
	for _, name := range []string{"description", "volume_size", "integrations", "configured_components", "effective_components", "name", "status", "created_at", "created_by", "updated_at", "updated_by"} {
		if !response.Schema.Attributes[name].IsComputed() {
			t.Fatalf("%s must be computed", name)
		}
	}
	if _, exists := response.Schema.Attributes["release_type"]; exists {
		t.Fatal("release_type is not observable and must not be exposed")
	}
}

func TestRecipeDataSourcesRejectMalformedSelectors(t *testing.T) {
	var diagnostics diag.Diagnostics
	validateRecipeSelectors(&diagnostics, false,
		recipeSelector{"project_id", types.StringValue("../project")},
		recipeSelector{"recipe_id", types.StringValue("recipe-1")},
	)
	if !diagnostics.HasError() {
		t.Fatal("expected malformed project selector to fail validation")
	}
	var missing diag.Diagnostics
	validateRecipeSelectors(&missing, false, recipeSelector{"version_id", types.StringNull()})
	if !missing.HasError() {
		t.Fatal("expected null selector to fail validation")
	}
	var whitespace diag.Diagnostics
	validateRecipeSelectors(&whitespace, false, recipeSelector{"recipe_id", types.StringValue(" recipe ")})
	if !whitespace.HasError() {
		t.Fatal("expected surrounding whitespace to fail validation")
	}
	if len(whitespace) != 1 || !strings.Contains(whitespace[0].Detail(), "recipe_id") {
		t.Fatalf("selector diagnostics must follow deterministic selector order: %v", whitespace)
	}
}

func TestRecipeSelectorValidationDefersUnknownOnlyUntilRead(t *testing.T) {
	selectors := []recipeSelector{
		{"project_id", types.StringUnknown()},
		{"recipe_id", types.StringValue("recipe-1")},
	}
	var configDiagnostics diag.Diagnostics
	validateRecipeSelectors(&configDiagnostics, true, selectors...)
	if configDiagnostics.HasError() {
		t.Fatalf("unknown selector should be deferred during config validation: %v", configDiagnostics)
	}
	var readDiagnostics diag.Diagnostics
	validateRecipeSelectors(&readDiagnostics, false, selectors...)
	if !readDiagnostics.HasError() {
		t.Fatal("unknown selector must fail when read is about to execute")
	}
}

func TestRecipeDataSourceRejectsRemoteIdentityMismatch(t *testing.T) {
	if err := validateRecipeRemoteIdentity("requested", vewrecipes.Recipe{ID: "other"}); err == nil {
		t.Fatal("expected recipe ID mismatch to fail")
	}
	if err := validateRecipeRemoteIdentity("requested", vewrecipes.Recipe{ID: "requested"}); err != nil {
		t.Fatalf("matching recipe ID rejected: %v", err)
	}
	if err := validateRecipeVersionRemoteIdentity("recipe", "version", vewrecipes.RecipeVersion{RecipeID: "other", ID: "version"}); err == nil {
		t.Fatal("expected recipe parent mismatch to fail")
	}
	if err := validateRecipeVersionRemoteIdentity("recipe", "version", vewrecipes.RecipeVersion{RecipeID: "recipe", ID: "other"}); err == nil {
		t.Fatal("expected version ID mismatch to fail")
	}
	if err := validateRecipeVersionRemoteIdentity("recipe", "version", vewrecipes.RecipeVersion{RecipeID: "recipe", ID: "version"}); err != nil {
		t.Fatalf("matching recipe version IDs rejected: %v", err)
	}
}

func TestRecipeDataSourceReadRejectsInvalidSelectorWithoutAPIRequest(t *testing.T) {
	ds := &recipeDataSource{client: &recipeReadDataSourceStub{remote: vewrecipes.Recipe{ID: "recipe"}}}
	var schemaResponse datasource.SchemaResponse
	ds.Schema(context.Background(), datasource.SchemaRequest{}, &schemaResponse)
	config := dataSourceConfig(context.Background(), schemaResponse.Schema, map[string]string{"project_id": "project", "recipe_id": " recipe"})
	response := datasource.ReadResponse{State: tfsdk.State{Schema: schemaResponse.Schema}}
	ds.Read(context.Background(), datasource.ReadRequest{Config: config}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected invalid selector diagnostics")
	}
	if ds.client.(*recipeReadDataSourceStub).calls != 0 {
		t.Fatal("invalid selector made an API request")
	}
}

func TestRecipeDataSourceReadHandlesNotFoundAndSanitizesPermissionFailure(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want []string
		omit []string
	}{
		{name: "not found", err: &vew.APIError{Status: 404}, want: []string{"not found", "recipe", "project"}},
		{
			name: "permission denied",
			err: &vew.APIError{Status: 403, Problem: vew.Problem{
				Code: "ACCESS_DENIED", RequestID: "request-403", Title: "Bearer hidden-token", Detail: "raw body with credentials",
			}},
			want: []string{"HTTP status 403", "ACCESS_DENIED", "request-403", "recipe"},
			omit: []string{"Bearer", "hidden-token", "raw body", "credentials"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &recipeReadDataSourceStub{err: test.err}
			ds := &recipeDataSource{client: stub}
			var schemaResponse datasource.SchemaResponse
			ds.Schema(context.Background(), datasource.SchemaRequest{}, &schemaResponse)
			config := dataSourceConfig(context.Background(), schemaResponse.Schema, map[string]string{"project_id": "project", "recipe_id": "recipe"})
			response := datasource.ReadResponse{State: tfsdk.State{Schema: schemaResponse.Schema}}
			ds.Read(context.Background(), datasource.ReadRequest{Config: config}, &response)
			if !response.Diagnostics.HasError() || stub.calls != 1 {
				t.Fatalf("diagnostics/calls = %v/%d, want error and one read", response.Diagnostics, stub.calls)
			}
			message := response.Diagnostics[0].Summary() + " " + response.Diagnostics[0].Detail()
			for _, value := range test.want {
				if !strings.Contains(message, value) {
					t.Errorf("diagnostic missing %q: %s", value, message)
				}
			}
			for _, value := range test.omit {
				if strings.Contains(message, value) {
					t.Errorf("diagnostic leaked %q: %s", value, message)
				}
			}
		})
	}
}

func TestRecipeVersionDataSourceReadRejectsIdentityMismatchBeforeState(t *testing.T) {
	stub := &recipeVersionReadDataSourceStub{remote: vewrecipes.RecipeVersion{ID: "other", RecipeID: "recipe", VolumeSize: "30"}}
	ds := &recipeVersionDataSource{client: stub}
	var schemaResponse datasource.SchemaResponse
	ds.Schema(context.Background(), datasource.SchemaRequest{}, &schemaResponse)
	config := dataSourceConfig(context.Background(), schemaResponse.Schema, map[string]string{"project_id": "project", "recipe_id": "recipe", "version_id": "version"})
	response := datasource.ReadResponse{State: tfsdk.State{Schema: schemaResponse.Schema}}
	ds.Read(context.Background(), datasource.ReadRequest{Config: config}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected mismatched remote identity diagnostics")
	}
	if stub.calls != 1 {
		t.Fatalf("API calls = %d, want one read", stub.calls)
	}
	var state recipeVersionDataSourceModel
	if diagnostics := response.State.Get(context.Background(), &state); !diagnostics.HasError() && !state.VersionID.IsNull() {
		t.Fatalf("mismatched result wrote state: %#v", state)
	}
}

func TestRecipeDataSourceReadRejectsIdentityMismatchBeforeState(t *testing.T) {
	stub := &recipeReadDataSourceStub{remote: vewrecipes.Recipe{ID: "other"}}
	ds := &recipeDataSource{client: stub}
	var schemaResponse datasource.SchemaResponse
	ds.Schema(context.Background(), datasource.SchemaRequest{}, &schemaResponse)
	config := dataSourceConfig(context.Background(), schemaResponse.Schema, map[string]string{"project_id": "project", "recipe_id": "recipe"})
	response := datasource.ReadResponse{State: tfsdk.State{Schema: schemaResponse.Schema}}
	ds.Read(context.Background(), datasource.ReadRequest{Config: config}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected mismatched remote identity diagnostics")
	}
	if stub.calls != 1 {
		t.Fatalf("API calls = %d, want one read", stub.calls)
	}
	var state recipeDataSourceModel
	if diagnostics := response.State.Get(context.Background(), &state); !diagnostics.HasError() && !state.RecipeID.IsNull() {
		t.Fatalf("mismatched result wrote state: %#v", state)
	}
}

func TestRecipeVersionDataSourceReadRejectsParentMismatchBeforeState(t *testing.T) {
	stub := &recipeVersionReadDataSourceStub{remote: vewrecipes.RecipeVersion{ID: "version", RecipeID: "other", VolumeSize: "30"}}
	ds := &recipeVersionDataSource{client: stub}
	var schemaResponse datasource.SchemaResponse
	ds.Schema(context.Background(), datasource.SchemaRequest{}, &schemaResponse)
	config := dataSourceConfig(context.Background(), schemaResponse.Schema, map[string]string{"project_id": "project", "recipe_id": "recipe", "version_id": "version"})
	response := datasource.ReadResponse{State: tfsdk.State{Schema: schemaResponse.Schema}}
	ds.Read(context.Background(), datasource.ReadRequest{Config: config}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected parent ID mismatch diagnostics")
	}
	if stub.calls != 1 {
		t.Fatalf("API calls = %d, want one read", stub.calls)
	}
	var state recipeVersionDataSourceModel
	if diagnostics := response.State.Get(context.Background(), &state); !diagnostics.HasError() && !state.VersionID.IsNull() {
		t.Fatalf("mismatched result wrote state: %#v", state)
	}
}

func TestRecipeVersionDataSourceReadKeepsTerminalStateAndConfiguredHistory(t *testing.T) {
	ctx := context.Background()
	selections := []struct {
		name       string
		configured *[]vewrecipes.ComponentVersion
		wantNull   bool
	}{
		{name: "historical missing selection", configured: nil, wantNull: true},
		{name: "explicit empty selection", configured: func() *[]vewrecipes.ComponentVersion {
			empty := []vewrecipes.ComponentVersion{}
			return &empty
		}()},
	}
	for _, selection := range selections {
		t.Run(selection.name, func(t *testing.T) {
			stub := &recipeVersionReadDataSourceStub{remote: vewrecipes.RecipeVersion{
				ID: "version", RecipeID: "recipe", Description: "historical", VolumeSize: "30",
				Components: selection.configured, EffectiveComponents: []vewrecipes.ComponentVersion{
					{ComponentID: "second", ComponentName: "Second", VersionID: "v2", VersionName: "2", Type: "MAIN", Order: 2},
					{ComponentID: "first", ComponentName: "First", VersionID: "v1", VersionName: "1", Type: "BASE", Order: 1},
				}, Status: "RETIRED",
			}}
			ds := &recipeVersionDataSource{client: stub}
			var schemaResponse datasource.SchemaResponse
			ds.Schema(ctx, datasource.SchemaRequest{}, &schemaResponse)
			config := dataSourceConfig(ctx, schemaResponse.Schema, map[string]string{"project_id": "project", "recipe_id": "recipe", "version_id": "version"})
			response := datasource.ReadResponse{State: tfsdk.State{Schema: schemaResponse.Schema}}
			ds.Read(ctx, datasource.ReadRequest{Config: config}, &response)
			if response.Diagnostics.HasError() || stub.calls != 1 {
				t.Fatalf("read diagnostics/calls = %v/%d", response.Diagnostics, stub.calls)
			}
			var state recipeVersionDataSourceModel
			if diagnostics := response.State.Get(ctx, &state); diagnostics.HasError() {
				t.Fatalf("decode data source state: %v", diagnostics)
			}
			if state.Status.ValueString() != "RETIRED" || state.RecipeID.ValueString() != "recipe" || state.VersionID.ValueString() != "version" {
				t.Fatalf("terminal version was not retained: %#v", state)
			}
			if state.ConfiguredComponents.IsNull() != selection.wantNull {
				t.Fatalf("configured components null=%t, want %t", state.ConfiguredComponents.IsNull(), selection.wantNull)
			}
			if !selection.wantNull && len(state.ConfiguredComponents.Elements()) != 0 {
				t.Fatalf("explicit empty configured components became %#v", state.ConfiguredComponents)
			}
			var effective []recipeComponentModel
			if diagnostics := state.EffectiveComponents.ElementsAs(ctx, &effective, false); diagnostics.HasError() {
				t.Fatalf("decode effective components: %v", diagnostics)
			}
			if len(effective) != 2 || effective[0].ComponentID.ValueString() != "second" || effective[1].ComponentID.ValueString() != "first" {
				t.Fatalf("effective ordering changed: %#v", effective)
			}
			if state.Integrations.IsNull() || len(state.Integrations.Elements()) != 0 || state.Name.ValueString() != "" || state.CreatedAt.ValueString() != "" {
				t.Fatalf("historical absent fields were fabricated: integrations=%v name=%v created_at=%v", state.Integrations, state.Name, state.CreatedAt)
			}
			if state.Description.ValueString() != "historical" || state.VolumeSize.ValueInt64() != 30 {
				t.Fatalf("historical observable fields were lost: description=%q volume=%d", state.Description.ValueString(), state.VolumeSize.ValueInt64())
			}
		})
	}
}

func dataSourceConfig(ctx context.Context, schema schema.Schema, selectors map[string]string) tfsdk.Config {
	values := make(map[string]tftypes.Value, len(schema.Attributes))
	for name, attribute := range schema.Attributes {
		var value any
		if selector, ok := selectors[name]; ok {
			value = selector
		}
		values[name] = tftypes.NewValue(attribute.GetType().TerraformType(ctx), value)
	}
	return tfsdk.Config{Schema: schema, Raw: tftypes.NewValue(schema.Type().TerraformType(ctx), values)}
}

func TestRecipeDataSourceFailureDiagnosticIsSafe(t *testing.T) {
	secret := "Bearer eyJhbGciOiJIUzI1NiJ9.secret"
	err := &vew.APIError{Status: 403, Problem: vew.Problem{
		Code: "ACCESS_DENIED", RequestID: "request-123", Title: secret, Detail: "body=" + secret,
	}}
	diagnostic := safeRecipeReadDiagnostic("read recipe version", err, "version-123")
	for _, safe := range []string{"read recipe version", "HTTP status 403", "ACCESS_DENIED", "request-123", "version-123"} {
		if !strings.Contains(diagnostic, safe) {
			t.Fatalf("diagnostic missing safe detail %q: %q", safe, diagnostic)
		}
	}
	for _, unsafe := range []string{"Bearer", "eyJhbGci", "body="} {
		if strings.Contains(diagnostic, unsafe) {
			t.Fatalf("diagnostic leaked %q: %q", unsafe, diagnostic)
		}
	}
	transportDiagnostic := safeRecipeReadDiagnostic("read recipe", errors.New(secret), "invalid/id")
	if strings.Contains(transportDiagnostic, "Bearer") || strings.Contains(transportDiagnostic, "invalid/id") {
		t.Fatalf("transport diagnostic leaked unsafe detail: %q", transportDiagnostic)
	}
}

func TestRecipeVersionDataSourcePreservesHistoricalAndOrderedComponents(t *testing.T) {
	ctx := context.Background()
	remote := vewrecipes.RecipeVersion{Components: nil, EffectiveComponents: []vewrecipes.ComponentVersion{
		{ComponentID: "c2", VersionID: "v2", Order: 2}, {ComponentID: "c1", VersionID: "v1", Order: 1},
	}}
	configured := types.ListNull(types.ObjectType{AttrTypes: recipeComponentTypes})
	effective, diagnostics := recipeComponentList(ctx, remote.EffectiveComponents)
	if diagnostics.HasError() {
		t.Fatalf("effective component conversion failed: %v", diagnostics)
	}
	if !configured.IsNull() {
		t.Fatal("missing historical configured selection must remain null")
	}
	var values []recipeComponentModel
	if d := effective.ElementsAs(ctx, &values, false); d.HasError() {
		t.Fatal(d)
	}
	if len(values) != 2 || values[0].ComponentID.ValueString() != "c2" || values[1].ComponentID.ValueString() != "c1" {
		t.Fatalf("effective component order changed: %#v", values)
	}
	explicitEmpty := []vewrecipes.ComponentVersion{}
	explicit, diagnostics := recipeComponentList(ctx, explicitEmpty)
	if diagnostics.HasError() || explicit.IsNull() || len(explicit.Elements()) != 0 {
		t.Fatalf("explicit empty selection must remain a non-null empty list: %v %v", explicit, diagnostics)
	}
}
