package components

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	vewcomponents "github.com/elva-labs/terraform-provider-vew/internal/vew/components"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

type componentReadDataSourceStub struct {
	remote vewcomponents.Component
	err    error
	calls  int
}

func (s *componentReadDataSourceStub) GetComponent(_ context.Context, _, _ string) (vewcomponents.Component, error) {
	s.calls++
	return s.remote, s.err
}

type componentVersionReadDataSourceStub struct {
	remote vewcomponents.ComponentVersion
	err    error
	calls  int
}

func (s *componentVersionReadDataSourceStub) GetComponentVersion(_ context.Context, _, _, _ string) (vewcomponents.ComponentVersion, error) {
	s.calls++
	return s.remote, s.err
}

func TestComponentDataSourceReadMapsArchivedComponent(t *testing.T) {
	ctx := context.Background()
	stub := &componentReadDataSourceStub{remote: vewcomponents.Component{
		ID: "component", Name: "archive", Description: "historical", Platform: "Linux",
		SupportedArchitectures: []string{"arm64", "x86_64"}, SupportedOSVersions: []string{"Ubuntu 24"}, Status: "ARCHIVED",
		CreatedAt: "created", CreatedBy: "creator", UpdatedAt: "updated", UpdatedBy: "updater",
	}}
	ds := &componentDataSource{client: stub}
	var schemaResponse datasource.SchemaResponse
	ds.Schema(ctx, datasource.SchemaRequest{}, &schemaResponse)
	response := datasource.ReadResponse{State: tfsdk.State{Schema: schemaResponse.Schema}}
	ds.Read(ctx, datasource.ReadRequest{Config: componentDataSourceTestConfig(ctx, schemaResponse.Schema, map[string]string{
		"project_id": "project", "component_id": "component",
	})}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("Read diagnostics: %v", response.Diagnostics)
	}
	if stub.calls != 1 {
		t.Fatalf("API calls = %d, want one", stub.calls)
	}
	var state componentDataSourceModel
	response.Diagnostics.Append(response.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		t.Fatalf("state decode diagnostics: %v", response.Diagnostics)
	}
	if state.ComponentID.ValueString() != "component" || state.Status.ValueString() != "ARCHIVED" || state.Name.ValueString() != "archive" || state.Platform.ValueString() != "Linux" || state.CreatedBy.ValueString() != "creator" {
		t.Fatalf("component state mapping incomplete: %#v", state)
	}
}

func TestComponentDataSourceReadDiagnosticsForNotFoundAndForbidden(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want []string
		omit []string
	}{
		{name: "not found", err: &vew.APIError{Status: 404}, want: []string{"not found"}, omit: []string{"project-secret-482", "component-secret-123"}},
		{name: "forbidden sanitized", err: &vew.APIError{Status: 403, Problem: vew.Problem{Code: "ACCESS_DENIED", RequestID: "request-123", Detail: "Bearer secret raw response"}}, want: []string{"403", "ACCESS_DENIED", "request-123"}, omit: []string{"Bearer", "secret", "raw response"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			stub := &componentReadDataSourceStub{err: tc.err}
			ds := &componentDataSource{client: stub}
			var schemaResponse datasource.SchemaResponse
			ds.Schema(ctx, datasource.SchemaRequest{}, &schemaResponse)
			response := datasource.ReadResponse{State: tfsdk.State{Schema: schemaResponse.Schema}}
			ds.Read(ctx, datasource.ReadRequest{Config: componentDataSourceTestConfig(ctx, schemaResponse.Schema, map[string]string{
				"project_id": "project-secret-482", "component_id": "component-secret-123",
			})}, &response)
			if !response.Diagnostics.HasError() || stub.calls != 1 {
				t.Fatalf("diagnostics=%v, calls=%d; expected one failed request", response.Diagnostics, stub.calls)
			}
			detail := response.Diagnostics[0].Detail()
			for _, token := range tc.want {
				if !strings.Contains(detail, token) {
					t.Fatalf("diagnostic %q missing %q", detail, token)
				}
			}
			for _, token := range tc.omit {
				if strings.Contains(detail, token) {
					t.Fatalf("diagnostic leaked %q: %s", token, detail)
				}
			}
		})
	}
}

func TestComponentVersionDataSourceReadMapsTerminalVersions(t *testing.T) {
	ctx := context.Background()
	position := "PREPEND"
	for _, status := range []string{"RETIRED", "FAILED"} {
		t.Run(status, func(t *testing.T) {
			stub := &componentVersionReadDataSourceStub{remote: vewcomponents.ComponentVersion{
				ComponentID: "component", ID: "version", Description: "historical", Name: "1.0.0",
				Definition: json.RawMessage(`{"z":1,"a":2}`), Status: status,
				LicenseDashboard: nil, Notes: nil,
				Dependencies: []vewcomponents.Dependency{
					{ComponentID: "second", VersionID: "v2", Type: "HELPER", Order: 2},
					{ComponentID: "first", VersionID: "v1", Type: "BASE", Order: 1, Position: &position},
				},
			}}
			ds := &componentVersionDataSource{client: stub}
			var schemaResponse datasource.SchemaResponse
			ds.Schema(ctx, datasource.SchemaRequest{}, &schemaResponse)
			response := datasource.ReadResponse{State: tfsdk.State{Schema: schemaResponse.Schema}}
			ds.Read(ctx, datasource.ReadRequest{Config: componentDataSourceTestConfig(ctx, schemaResponse.Schema, map[string]string{
				"project_id": "project", "component_id": "component", "version_id": "version",
			})}, &response)
			if response.Diagnostics.HasError() {
				t.Fatalf("Read diagnostics: %v", response.Diagnostics)
			}
			if stub.calls != 1 {
				t.Fatalf("API calls = %d, want one", stub.calls)
			}
			var state componentVersionDataSourceModel
			response.Diagnostics.Append(response.State.Get(ctx, &state)...)
			if response.Diagnostics.HasError() {
				t.Fatalf("state decode diagnostics: %v", response.Diagnostics)
			}
			if state.Status.ValueString() != status || state.DefinitionJSON.ValueString() != `{"a":2,"z":1}` || !state.Notes.IsNull() || !state.LicenseDashboard.IsNull() {
				t.Fatalf("version state mapping incorrect: %#v", state)
			}
			var dependencies []dependencyModel
			response.Diagnostics.Append(state.Dependencies.ElementsAs(ctx, &dependencies, false)...)
			if response.Diagnostics.HasError() || len(dependencies) != 2 || dependencies[0].ComponentID.ValueString() != "first" || dependencies[0].Position.ValueString() != "PREPEND" || dependencies[1].ComponentID.ValueString() != "second" {
				t.Fatalf("ordered dependency mapping incorrect: %#v, diagnostics=%v", dependencies, response.Diagnostics)
			}
		})
	}
}

func componentDataSourceTestConfig(ctx context.Context, schema schema.Schema, selectors map[string]string) tfsdk.Config {
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
