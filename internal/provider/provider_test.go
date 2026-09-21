package provider

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/elva-labs/terraform-provider-vew/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/provider"
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
		want := client.Config{APIURL: "https://configured.example/api", TokenURL: "https://configured.example/token", ClientID: "configured-client", ClientSecret: secret}
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

func TestProviderConfigureSetsComponentAPI(t *testing.T) {
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
	if _, ok := response.ResourceData.(client.ComponentAPI); !ok {
		t.Fatalf("expected ResourceData to contain client.ComponentAPI, got %T", response.ResourceData)
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
