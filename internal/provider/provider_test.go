package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elva-labs/terraform-provider-vew/internal/providerdata"
	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	"github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestProviderMetadata(t *testing.T) {
	t.Parallel()

	var response provider.MetadataResponse
	New("test")().Metadata(context.Background(), provider.MetadataRequest{}, &response)

	if response.TypeName != "vew" {
		t.Fatalf("expected provider type name %q, got %q", "vew", response.TypeName)
	}
	if response.Version != "test" {
		t.Fatalf("expected provider version %q, got %q", "test", response.Version)
	}
}

func TestProviderSchema(t *testing.T) {
	t.Parallel()

	var response provider.SchemaResponse
	New("test")().Schema(context.Background(), provider.SchemaRequest{}, &response)

	wantAttributes := map[string]bool{
		"api_url":       true,
		"token_url":     true,
		"client_id":     true,
		"client_secret": true,
	}
	if len(response.Schema.Attributes) != len(wantAttributes) {
		t.Fatalf("expected %d provider attributes, got %d", len(wantAttributes), len(response.Schema.Attributes))
	}
	for name := range wantAttributes {
		attribute, ok := response.Schema.Attributes[name]
		if !ok {
			t.Fatalf("expected provider attribute %q", name)
		}
		if !attribute.IsOptional() {
			t.Fatalf("expected provider attribute %q to be optional", name)
		}
	}
	if !response.Schema.Attributes["client_secret"].IsSensitive() {
		t.Fatal("expected client_secret to be sensitive")
	}
}

func TestProviderConfig(t *testing.T) {
	t.Parallel()

	const secret = "test-secret"
	base := providerModel{
		APIURL:       types.StringValue("https://configured.example/api"),
		TokenURL:     types.StringValue("https://configured.example/token"),
		ClientID:     types.StringValue("configured-client"),
		ClientSecret: types.StringValue(secret),
	}

	t.Run("explicit values", func(t *testing.T) {
		got, diags := resolveProviderConfig(base, func(string) (string, bool) { return "", false })
		assertNoDiagnostics(t, diags)
		want := vew.Config{APIURL: "https://configured.example/api", TokenURL: "https://configured.example/token", ClientID: "configured-client", ClientSecret: secret}
		if got != want {
			t.Fatalf("expected %#v, got %#v", want, got)
		}
	})

	t.Run("environment-only values", func(t *testing.T) {
		env := map[string]string{
			"VEW_API_URL": "https://env.example/api", "VEW_TOKEN_URL": "https://env.example/token",
			"VEW_CLIENT_ID": "env-client", "VEW_CLIENT_SECRET": secret,
		}
		got, diags := resolveProviderConfig(providerModel{}, mapGetenv(env))
		assertNoDiagnostics(t, diags)
		if got.APIURL != env["VEW_API_URL"] || got.TokenURL != env["VEW_TOKEN_URL"] || got.ClientID != env["VEW_CLIENT_ID"] || got.ClientSecret != secret {
			t.Fatalf("unexpected config: %#v", got)
		}
	})

	t.Run("explicit values take precedence", func(t *testing.T) {
		env := map[string]string{
			"VEW_API_URL": "https://env.example/api", "VEW_TOKEN_URL": "https://env.example/token",
			"VEW_CLIENT_ID": "env-client", "VEW_CLIENT_SECRET": "other-secret",
		}
		got, diags := resolveProviderConfig(base, mapGetenv(env))
		assertNoDiagnostics(t, diags)
		if got.APIURL != base.APIURL.ValueString() || got.TokenURL != base.TokenURL.ValueString() || got.ClientID != base.ClientID.ValueString() || got.ClientSecret != secret {
			t.Fatalf("unexpected config: %#v", got)
		}
	})

	for _, field := range []struct {
		name string
		set  func(*providerModel)
		env  string
	}{
		{"api_url", func(m *providerModel) { m.APIURL = types.StringNull() }, "VEW_API_URL"},
		{"token_url", func(m *providerModel) { m.TokenURL = types.StringNull() }, "VEW_TOKEN_URL"},
		{"client_id", func(m *providerModel) { m.ClientID = types.StringNull() }, "VEW_CLIENT_ID"},
		{"client_secret", func(m *providerModel) { m.ClientSecret = types.StringNull() }, "VEW_CLIENT_SECRET"},
	} {
		field := field
		t.Run("missing "+field.name, func(t *testing.T) {
			model := base
			field.set(&model)
			_, diags := resolveProviderConfig(model, func(string) (string, bool) { return "", false })
			assertDiagnosticContains(t, diags, field.name)
			assertDiagnosticContains(t, diags, field.env)
			assertDiagnosticsOmit(t, diags, secret)
		})
	}

	for _, field := range []struct {
		name string
		set  func(*providerModel)
	}{
		{"api_url", func(m *providerModel) { m.APIURL = types.StringValue("relative") }},
		{"token_url", func(m *providerModel) { m.TokenURL = types.StringValue("ftp://example.invalid/token") }},
	} {
		field := field
		t.Run("invalid "+field.name+" URL", func(t *testing.T) {
			model := base
			field.set(&model)
			_, diags := resolveProviderConfig(model, func(string) (string, bool) { return "", false })
			assertDiagnosticContains(t, diags, field.name)
			assertDiagnosticsOmit(t, diags, secret)
		})
	}

	t.Run("unknown values are rejected", func(t *testing.T) {
		model := base
		model.APIURL = types.StringUnknown()
		_, diags := resolveProviderConfig(model, func(string) (string, bool) { return "https://env.example/api", true })
		assertDiagnosticContains(t, diags, "api_url")
		assertDiagnosticsOmit(t, diags, secret)
	})
}

func TestProviderConfigureSetsProviderData(t *testing.T) {
	t.Parallel()

	p := New("test")()
	var schemaResponse provider.SchemaResponse
	p.Schema(context.Background(), provider.SchemaRequest{}, &schemaResponse)
	request := provider.ConfigureRequest{Config: tfsdk.Config{
		Schema: schemaResponse.Schema,
		Raw: tftypes.NewValue(schemaResponse.Schema.Type().TerraformType(context.Background()), map[string]tftypes.Value{
			"api_url":       tftypes.NewValue(tftypes.String, "https://configured.example/api"),
			"token_url":     tftypes.NewValue(tftypes.String, "https://configured.example/token"),
			"client_id":     tftypes.NewValue(tftypes.String, "configured-client"),
			"client_secret": tftypes.NewValue(tftypes.String, "test-secret"),
		})}}
	var response provider.ConfigureResponse
	p.Configure(context.Background(), request, &response)
	assertNoDiagnostics(t, response.Diagnostics)
	data, ok := response.ResourceData.(providerdata.Data)
	if !ok || data.Components == nil {
		t.Fatalf("expected ResourceData to contain providerdata.Data with components API, got %T", response.ResourceData)
	}
	if data.ComponentVersions == nil || data.Pipelines == nil || data.Recipes == nil || data.RecipeVersions == nil || data.Waiter == nil {
		t.Fatalf("expected ResourceData to include version APIs, pipeline API, recipe API, and waiter, got %#v", data)
	}
	if data.ComponentVersionReleases == nil || data.RecipeVersionReleases == nil {
		t.Fatalf("expected ResourceData to include release APIs, got %#v", data)
	}
	if _, ok := response.DataSourceData.(providerdata.Data); !ok {
		t.Fatalf("expected DataSourceData to contain providerdata.Data, got %T", response.DataSourceData)
	}
	if _, ok := response.ActionData.(providerdata.Data); !ok {
		t.Fatalf("expected ActionData to contain providerdata.Data, got %T", response.ActionData)
	}
}

func TestProviderReleaseActionsUseOnlyReleaseScopes(t *testing.T) {
	var requestedScopes []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/oauth/token":
			if err := request.ParseForm(); err != nil {
				t.Errorf("parse token form: %v", err)
				return
			}
			requestedScopes = append(requestedScopes, request.Form.Get("scope"))
			_, _ = fmt.Fprint(w, `{"access_token":"test-token","expires_in":3600}`)
		case "/api/projects/project-1/components/component-1/versions/version-1/release":
			_, _ = fmt.Fprint(w, `{"componentVersionId":"version-1"}`)
		case "/api/projects/project-1/recipes/recipe-1/versions/version-1/release":
			_, _ = fmt.Fprint(w, `{"recipeVersionId":"version-1"}`)
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()

	p := New("test")()
	var schemaResponse provider.SchemaResponse
	p.Schema(context.Background(), provider.SchemaRequest{}, &schemaResponse)
	request := provider.ConfigureRequest{Config: tfsdk.Config{
		Schema: schemaResponse.Schema,
		Raw: tftypes.NewValue(schemaResponse.Schema.Type().TerraformType(context.Background()), map[string]tftypes.Value{
			"api_url":       tftypes.NewValue(tftypes.String, server.URL+"/api"),
			"token_url":     tftypes.NewValue(tftypes.String, server.URL+"/oauth/token"),
			"client_id":     tftypes.NewValue(tftypes.String, "configured-client"),
			"client_secret": tftypes.NewValue(tftypes.String, "test-secret"),
		})}}
	var response provider.ConfigureResponse
	p.Configure(context.Background(), request, &response)
	assertNoDiagnostics(t, response.Diagnostics)
	data := response.ActionData.(providerdata.Data)
	if err := data.ComponentVersionReleases.ReleaseComponentVersion(context.Background(), "project-1", "component-1", "version-1"); err != nil {
		t.Fatalf("release component version: %v", err)
	}
	if err := data.RecipeVersionReleases.ReleaseRecipeVersion(context.Background(), "project-1", "recipe-1", "version-1"); err != nil {
		t.Fatalf("release recipe version: %v", err)
	}
	want := []string{"clients/packaging/component.release", "clients/packaging/recipe.release"}
	if len(requestedScopes) != len(want) {
		t.Fatalf("requested scopes = %q, want %q", requestedScopes, want)
	}
	for index := range want {
		if requestedScopes[index] != want[index] {
			t.Fatalf("requested scope[%d] = %q, want %q", index, requestedScopes[index], want[index])
		}
	}
}

func TestProviderPipelineUsesOnlyPipelineScopes(t *testing.T) {
	var requestedScope string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/oauth/token":
			if err := request.ParseForm(); err != nil {
				t.Errorf("parse token form: %v", err)
				return
			}
			requestedScope = request.Form.Get("scope")
			_, _ = fmt.Fprint(w, `{"access_token":"test-token","expires_in":3600}`)
		case "/api/projects/project-1/pipelines/pipe-1":
			_, _ = fmt.Fprint(w, `{"pipeline":{"projectId":"project-1","pipelineId":"pipe-1","pipelineName":"test","pipelineDescription":"test","recipeId":"reci-1","recipeName":"recipe","recipeVersionId":"vers-1","recipeVersionName":"1.0.0","buildInstanceTypes":["m8i.2xlarge"],"pipelineSchedule":"0 0 * * ? *","status":"CREATED","createDate":"2026-01-01","createdBy":"test","lastUpdateDate":"2026-01-01","lastUpdatedBy":"test"}}`)
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()

	p := New("test")()
	var schemaResponse provider.SchemaResponse
	p.Schema(context.Background(), provider.SchemaRequest{}, &schemaResponse)
	request := provider.ConfigureRequest{Config: tfsdk.Config{
		Schema: schemaResponse.Schema,
		Raw: tftypes.NewValue(schemaResponse.Schema.Type().TerraformType(context.Background()), map[string]tftypes.Value{
			"api_url":       tftypes.NewValue(tftypes.String, server.URL+"/api"),
			"token_url":     tftypes.NewValue(tftypes.String, server.URL+"/oauth/token"),
			"client_id":     tftypes.NewValue(tftypes.String, "configured-client"),
			"client_secret": tftypes.NewValue(tftypes.String, "test-secret"),
		})}}
	var response provider.ConfigureResponse
	p.Configure(context.Background(), request, &response)
	assertNoDiagnostics(t, response.Diagnostics)
	data := response.ResourceData.(providerdata.Data)
	if _, err := data.Pipelines.GetPipeline(context.Background(), "project-1", "pipe-1"); err != nil {
		t.Fatalf("read pipeline: %v", err)
	}
	if want := "clients/packaging/pipeline.read clients/packaging/pipeline.write"; requestedScope != want {
		t.Fatalf("pipeline OAuth scope = %q, want %q", requestedScope, want)
	}
}

func TestProviderResourcesIncludesPipelines(t *testing.T) {
	t.Parallel()

	resources := New("test")().Resources(context.Background())
	if len(resources) != 5 {
		t.Fatalf("resource constructors = %d, want 5", len(resources))
	}
	want := []string{"vew_component", "vew_component_version", "vew_pipeline", "vew_recipe", "vew_recipe_version"}
	for index, constructor := range resources {
		var response resource.MetadataResponse
		constructor().Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "vew"}, &response)
		if response.TypeName != want[index] {
			t.Fatalf("resource[%d] type = %q, want %q", index, response.TypeName, want[index])
		}
	}
}

func TestProviderActionsIncludesVersionReleases(t *testing.T) {
	t.Parallel()

	providerWithActions, ok := New("test")().(provider.ProviderWithActions)
	if !ok {
		t.Fatal("provider does not implement provider.ProviderWithActions")
	}
	actions := providerWithActions.Actions(context.Background())
	if len(actions) != 2 {
		t.Fatalf("action constructors = %d, want 2", len(actions))
	}
	want := []string{"vew_component_version_release", "vew_recipe_version_release"}
	for index, constructor := range actions {
		var response action.MetadataResponse
		constructor().Metadata(context.Background(), action.MetadataRequest{ProviderTypeName: "vew"}, &response)
		if response.TypeName != want[index] {
			t.Fatalf("action[%d] type = %q, want %q", index, response.TypeName, want[index])
		}
	}
}

func mapGetenv(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}

func assertNoDiagnostics(t *testing.T, diags diag.Diagnostics) {
	t.Helper()
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %s", diagnosticsString(diags))
	}
}

func assertDiagnosticContains(t *testing.T, diags diag.Diagnostics, want string) {
	t.Helper()
	for _, diagnostic := range diags {
		if strings.Contains(diagnostic.Summary(), want) || strings.Contains(diagnostic.Detail(), want) {
			return
		}
	}
	t.Fatalf("expected diagnostic containing %q, got %s", want, diagnosticsString(diags))
}

func diagnosticsString(diags diag.Diagnostics) string { return fmt.Sprint(diags) }

func assertDiagnosticsOmit(t *testing.T, diags diag.Diagnostics, secret string) {
	t.Helper()
	for _, diagnostic := range diags {
		if strings.Contains(diagnostic.Summary(), secret) || strings.Contains(diagnostic.Detail(), secret) {
			t.Fatalf("diagnostic contains secret: %s", diagnostic.Detail())
		}
	}
}
