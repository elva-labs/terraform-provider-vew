package releaseactions

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/elva-labs/terraform-provider-vew/internal/providerdata"
	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	"github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/action/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

type componentStub struct {
	calls   [][3]string
	results []error
}

func (s *componentStub) ReleaseComponentVersion(_ context.Context, project, component, version string) error {
	s.calls = append(s.calls, [3]string{project, component, version})
	return s.next()
}

func (s *componentStub) next() error {
	if len(s.results) == 0 {
		return nil
	}
	err := s.results[0]
	s.results = s.results[1:]
	return err
}

type recipeStub struct {
	calls   [][3]string
	results []error
}

func (s *recipeStub) ReleaseRecipeVersion(_ context.Context, project, recipe, version string) error {
	s.calls = append(s.calls, [3]string{project, recipe, version})
	if len(s.results) == 0 {
		return nil
	}
	err := s.results[0]
	s.results = s.results[1:]
	return err
}

func actionConfig(t *testing.T, a action.Action, values map[string]any) tfsdk.Config {
	t.Helper()
	var response action.SchemaResponse
	a.Schema(context.Background(), action.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema: %v", response.Diagnostics)
	}
	raw := make(map[string]tftypes.Value, len(response.Schema.Attributes))
	for name := range response.Schema.Attributes {
		value, ok := values[name]
		if !ok {
			value = nil
		}
		raw[name] = tftypes.NewValue(tftypes.String, value)
	}
	return tfsdk.Config{Schema: response.Schema, Raw: tftypes.NewValue(response.Schema.Type().TerraformType(context.Background()), raw)}
}

func invoke(t *testing.T, a action.Action, values map[string]any) action.InvokeResponse {
	t.Helper()
	var response action.InvokeResponse
	a.Invoke(context.Background(), action.InvokeRequest{Config: actionConfig(t, a, values)}, &response)
	return response
}

func configure(t *testing.T, a action.Action, component *componentStub, recipe *recipeStub) {
	t.Helper()
	var response action.ConfigureResponse
	a.(action.ActionWithConfigure).Configure(context.Background(), action.ConfigureRequest{ProviderData: providerdata.Data{
		ComponentVersionReleases: component, RecipeVersionReleases: recipe,
	}}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("configure: %v", response.Diagnostics)
	}
}

func TestReleaseActionSchemasAndInvocation(t *testing.T) {
	for _, tc := range []struct {
		name, parent string
		newAction    func() action.Action
	}{
		{"component", "component_id", NewComponentVersionReleaseAction},
		{"recipe", "recipe_id", NewRecipeVersionReleaseAction},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := tc.newAction()
			var metadata action.MetadataResponse
			a.Metadata(context.Background(), action.MetadataRequest{ProviderTypeName: "vew"}, &metadata)
			if metadata.TypeName != "vew_"+tc.name+"_version_release" {
				t.Fatalf("type name = %q", metadata.TypeName)
			}
			var declaration action.SchemaResponse
			a.Schema(context.Background(), action.SchemaRequest{}, &declaration)
			if len(declaration.Schema.Attributes) != 3 {
				t.Fatalf("attributes = %v", declaration.Schema.Attributes)
			}
			for _, name := range []string{"project_id", tc.parent, "version_id"} {
				attribute, ok := declaration.Schema.Attributes[name].(schema.StringAttribute)
				if !ok || !attribute.Required {
					t.Errorf("%s must be a required string attribute", name)
				}
			}
			component, recipe := &componentStub{}, &recipeStub{}
			configure(t, a, component, recipe)
			values := map[string]any{"project_id": "project", tc.parent: "parent", "version_id": "version"}
			for _, field := range []string{"project_id", tc.parent, "version_id"} {
				for _, invalid := range []any{nil, "", "  ", tftypes.UnknownValue} {
					bad := map[string]any{"project_id": "project", tc.parent: "parent", "version_id": "version"}
					bad[field] = invalid
					if response := invoke(t, a, bad); !response.Diagnostics.HasError() {
						t.Errorf("%s=%v should fail", field, invalid)
					}
				}
			}
			if len(component.calls)+len(recipe.calls) != 0 {
				t.Fatal("invalid IDs reached release endpoint")
			}
			if response := invoke(t, a, values); response.Diagnostics.HasError() {
				t.Fatalf("invoke: %v", response.Diagnostics)
			}
			if response := invoke(t, a, values); response.Diagnostics.HasError() {
				t.Fatalf("repeat invoke: %v", response.Diagnostics)
			}
			if tc.name == "component" {
				if len(component.calls) != 2 || component.calls[0] != [3]string{"project", "parent", "version"} || len(recipe.calls) != 0 {
					t.Fatalf("release calls: component=%v recipe=%v", component.calls, recipe.calls)
				}
			} else if len(recipe.calls) != 2 || recipe.calls[0] != [3]string{"project", "parent", "version"} || len(component.calls) != 0 {
				t.Fatalf("release calls: component=%v recipe=%v", component.calls, recipe.calls)
			}
		})
	}
}

func TestReleaseActionRejectedVersionAndLostResponse(t *testing.T) {
	denied := &vew.APIError{Status: 409, Problem: vew.Problem{Code: "INVALID_STATUS", RequestID: "request-1", Detail: "raw-secret"}}
	component := &componentStub{results: []error{denied, errors.New("Bearer secret"), nil}}
	a := NewComponentVersionReleaseAction()
	configure(t, a, component, nil)
	values := map[string]any{"project_id": "project", "component_id": "component", "version_id": "version"}
	for i := 0; i < 3; i++ {
		response := invoke(t, a, values)
		if (i < 2) != response.Diagnostics.HasError() {
			t.Fatalf("invoke %d diagnostics = %v", i, response.Diagnostics)
		}
	}
	if len(component.calls) != 3 {
		t.Fatalf("calls = %v", component.calls)
	}
}

func TestReleaseActionDiagnostics(t *testing.T) {
	secret := "Bearer ey.secret"
	for _, tc := range []struct {
		name string
		err  error
		want []string
		omit []string
	}{
		{"safe API", &vew.APIError{Status: 422, Problem: vew.Problem{Code: "DOMAIN_VALIDATION_FAILED", RequestID: "request-123", Detail: secret + " raw body"}}, []string{"release VEW component version", "project ID: project", "component ID: component", "version ID: version", "HTTP status 422", "DOMAIN_VALIDATION_FAILED", "request-123"}, []string{secret, "raw body"}},
		{"unsafe API fields", &vew.APIError{Status: 400, Problem: vew.Problem{Code: "BAD\n" + secret, RequestID: "request " + secret, Detail: secret}}, []string{"HTTP status 400"}, []string{"BAD", "request", secret}},
		{"transport", errors.New(secret + " raw response"), []string{"release VEW component version"}, []string{secret, "raw response"}},
		{"permission", &vew.APIError{Status: 403, Problem: vew.Problem{Code: "ACCESS_DENIED", Detail: secret}}, []string{"clients/packaging/component.release", "assigned to this project", "HTTP status 403"}, []string{secret}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			component := &componentStub{results: []error{tc.err}}
			a := NewComponentVersionReleaseAction()
			configure(t, a, component, nil)
			response := invoke(t, a, map[string]any{"project_id": "project", "component_id": "component", "version_id": "version"})
			if len(response.Diagnostics) != 1 || !response.Diagnostics.HasError() {
				t.Fatalf("diagnostics = %v", response.Diagnostics)
			}
			message := response.Diagnostics[0].Detail()
			for _, value := range tc.want {
				if !strings.Contains(message, value) {
					t.Errorf("missing %q in %q", value, message)
				}
			}
			for _, value := range tc.omit {
				if strings.Contains(message, value) {
					t.Errorf("leaked %q in %q", value, message)
				}
			}
			if len(component.calls) != 1 {
				t.Errorf("failed release must make one client call, got %v", component.calls)
			}
		})
	}
}

func TestReleaseDiagnosticOmitsUnsafeTargetIDs(t *testing.T) {
	secret := "Bearer secret"
	var diagnostics diag.Diagnostics
	addReleaseError(&diagnostics, "recipe", "project\n"+secret, "recipe "+secret, "version/"+secret, &vew.APIError{
		Status: 403, Problem: vew.Problem{Code: "ACCESS_DENIED", RequestID: "request-123"},
	})
	message := diagnostics[0].Detail()
	if strings.Contains(message, secret) || strings.Contains(message, "project ID:") || strings.Contains(message, "recipe ID:") || strings.Contains(message, "version ID:") {
		t.Fatalf("unsafe IDs leaked: %q", message)
	}
	for _, safe := range []string{"clients/packaging/recipe.release", "HTTP status 403", "ACCESS_DENIED", "request-123"} {
		if !strings.Contains(message, safe) {
			t.Errorf("missing %q from %q", safe, message)
		}
	}
}

func TestRecipePrerequisiteDiagnosticOnlyRecognizesExactValidation(t *testing.T) {
	valid := "Version component-version of component component has not been released."
	for _, tc := range []struct {
		name, detail, code string
		status             int
		recognized         bool
	}{
		{"exact", valid, "DOMAIN_VALIDATION_FAILED", 422, true},
		{"other code", valid, "OTHER", 422, false},
		{"other status", valid, "DOMAIN_VALIDATION_FAILED", 400, false},
		{"suffix", valid + " Bearer secret", "DOMAIN_VALIDATION_FAILED", 422, false},
		{"prefix", "Bearer secret " + valid, "DOMAIN_VALIDATION_FAILED", 422, false},
		{"newline", "Version component-version\n of component component has not been released.", "DOMAIN_VALIDATION_FAILED", 422, false},
		{"unsafe ID", "Version component/version of component component has not been released.", "DOMAIN_VALIDATION_FAILED", 422, false},
		{"recipe instead", "Version component-version of recipe component has not been released.", "DOMAIN_VALIDATION_FAILED", 422, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recipe := &recipeStub{results: []error{&vew.APIError{Status: tc.status, Problem: vew.Problem{Code: tc.code, Detail: tc.detail}}}}
			a := NewRecipeVersionReleaseAction()
			configure(t, a, nil, recipe)
			response := invoke(t, a, map[string]any{"project_id": "project", "recipe_id": "recipe", "version_id": "version"})
			if len(response.Diagnostics) != 1 {
				t.Fatalf("diagnostics = %v", response.Diagnostics)
			}
			message := response.Diagnostics[0].Detail()
			if strings.Contains(message, "Release that component version") != tc.recognized {
				t.Errorf("prerequisite recognition = %v, message = %q", tc.recognized, message)
			}
			if strings.Contains(message, "Bearer") {
				t.Errorf("secret leaked: %q", message)
			}
		})
	}
}
