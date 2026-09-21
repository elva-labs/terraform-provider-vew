package provider

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/elva-labs/terraform-provider-vew/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	testresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestComponentResourceSchema(t *testing.T) {
	t.Parallel()

	r := NewComponentResource()
	var response resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &response)

	expectComputedString := func(name string) {
		t.Helper()
		attribute, ok := response.Schema.Attributes[name].(resourceschema.StringAttribute)
		if !ok || !attribute.Computed || attribute.Required || attribute.Optional {
			t.Fatalf("%s = %#v, want computed string", name, response.Schema.Attributes[name])
		}
	}
	expectRequiredString := func(name string, replacement bool) {
		t.Helper()
		attribute, ok := response.Schema.Attributes[name].(resourceschema.StringAttribute)
		if !ok || !attribute.Required || attribute.Computed || attribute.Optional {
			t.Fatalf("%s = %#v, want required string", name, response.Schema.Attributes[name])
		}
		if got := len(attribute.PlanModifiers); (got > 0) != replacement {
			t.Fatalf("%s has %d plan modifiers, replacement = %t", name, got, replacement)
		}
	}
	expectRequiredSet := func(name string) {
		t.Helper()
		attribute, ok := response.Schema.Attributes[name].(resourceschema.SetAttribute)
		if !ok || !attribute.Required || attribute.Computed || attribute.Optional || attribute.ElementType != types.StringType {
			t.Fatalf("%s = %#v, want required set(string)", name, response.Schema.Attributes[name])
		}
		if len(attribute.PlanModifiers) == 0 {
			t.Fatalf("%s is missing replacement plan modifier", name)
		}
	}

	expectComputedString("id")
	expectRequiredString("project_id", true)
	expectRequiredString("name", true)
	expectRequiredString("description", false)
	expectRequiredString("platform", true)
	expectRequiredSet("supported_architectures")
	expectRequiredSet("supported_os_versions")
	for _, name := range []string{"status", "created_at", "created_by", "updated_at", "updated_by"} {
		expectComputedString(name)
	}
}

func TestComponentResourceOperationDiagnosticsAreSanitizedAndContextual(t *testing.T) {
	secret := "bearer-secret"
	apiErr := &client.APIError{Status: http.StatusBadRequest, Problem: client.Problem{
		Code: "INVALID_COMPONENT", RequestID: "request-123", Detail: "response body " + secret,
	}}
	for _, tc := range []struct {
		name      string
		operation string
		run       func(*componentResource, tfsdk.Plan, tfsdk.State) string
	}{
		{
			name: "create", operation: "create", run: func(r *componentResource, plan tfsdk.Plan, _ tfsdk.State) string {
				response := resource.CreateResponse{State: componentEmptyState(t, r)}
				r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &response)
				return diagnosticsString(response.Diagnostics)
			},
		},
		{
			name: "read", operation: "read", run: func(r *componentResource, _ tfsdk.Plan, state tfsdk.State) string {
				response := resource.ReadResponse{State: state}
				r.Read(context.Background(), resource.ReadRequest{State: state}, &response)
				return diagnosticsString(response.Diagnostics)
			},
		},
		{
			name: "update", operation: "update", run: func(r *componentResource, plan tfsdk.Plan, state tfsdk.State) string {
				response := resource.UpdateResponse{State: componentEmptyState(t, r)}
				r.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, &response)
				return diagnosticsString(response.Diagnostics)
			},
		},
		{
			name: "delete", operation: "archive", run: func(r *componentResource, _ tfsdk.Plan, state tfsdk.State) string {
				response := resource.DeleteResponse{}
				r.Delete(context.Background(), resource.DeleteRequest{State: state}, &response)
				return diagnosticsString(response.Diagnostics)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &componentResource{client: failingComponentAPI{err: apiErr}}
			plan := componentTestPlan(t, r)
			state := componentTestState(t, r, testComponent("CREATED"))
			got := tc.run(r, plan, state)
			for _, want := range []string{tc.operation, "400", "INVALID_COMPONENT", "request-123"} {
				if !strings.Contains(got, want) {
					t.Fatalf("diagnostic %q missing %q", got, want)
				}
			}
			if strings.Contains(got, secret) {
				t.Fatalf("diagnostic leaked secret: %q", got)
			}
		})
	}
}

type failingComponentAPI struct{ err error }

func (api failingComponentAPI) CreateComponent(context.Context, string, client.CreateComponentInput) (string, error) {
	return "", api.err
}

func (api failingComponentAPI) GetComponent(context.Context, string, string) (client.Component, error) {
	return client.Component{}, api.err
}

func (api failingComponentAPI) UpdateComponent(context.Context, string, string, client.UpdateComponentInput) error {
	return api.err
}

func (api failingComponentAPI) ArchiveComponent(context.Context, string, string) error {
	return api.err
}

func TestComponentResourceImport(t *testing.T) {
	t.Parallel()

	r := NewComponentResource()
	var schemaResponse resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &schemaResponse)

	response := resource.ImportStateResponse{State: tfsdk.State{
		Schema: schemaResponse.Schema,
		Raw:    tftypes.NewValue(schemaResponse.Schema.Type().TerraformType(context.Background()), nil),
	}}
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: "prog-73488/cmp-123"}, &response)
	assertNoDiagnostics(t, response.Diagnostics)

	var state componentModel
	assertNoDiagnostics(t, response.State.Get(context.Background(), &state))
	if state.ProjectID.ValueString() != "prog-73488" || state.ID.ValueString() != "cmp-123" {
		t.Fatalf("import state = %#v, want project prog-73488 and id cmp-123", state)
	}

	for _, id := range []string{"", "cmp-123", "/cmp-123", "prog-73488/", "prog/a/cmp-123"} {
		if _, _, err := parseComponentImportID(id); err == nil {
			t.Errorf("parseComponentImportID(%q) succeeded", id)
		}
	}
}

func TestComponentResourceLifecycle(t *testing.T) {
	fake := newFakeVEWServer(t)
	config := testProviderConfig(fake) + componentResourceConfig("created by provider test")

	testresource.Test(t, testresource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testProtoV6ProviderFactories(),
		CheckDestroy: func(*terraform.State) error {
			fake.mu.Lock()
			defer fake.mu.Unlock()
			if !fake.archived || fake.archiveCalls != 1 {
				return fmt.Errorf("archive state/calls = %t/%d, want true/1", fake.archived, fake.archiveCalls)
			}
			return nil
		},
		Steps: []testresource.TestStep{
			{
				Config: config,
				Check: testresource.ComposeTestCheckFunc(
					testresource.TestCheckResourceAttr("vew_component.test", "id", "cmp-123"),
					testresource.TestCheckResourceAttr("vew_component.test", "status", "CREATED"),
					testresource.TestCheckResourceAttr("vew_component.test", "created_by", "terraform-test-user"),
					func(*terraform.State) error {
						fake.mu.Lock()
						defer fake.mu.Unlock()
						if fake.createCalls != 1 || fake.getCalls != 1 || fake.idempotencyKey == "" || fake.create.Name != "terraform-test-component" || fake.create.Description != "created by provider test" || fake.create.Platform != "Linux" || len(fake.create.SupportedArchitectures) != 2 || fake.create.SupportedArchitectures[0] != "arm64" || fake.create.SupportedArchitectures[1] != "x86_64" || len(fake.create.SupportedOSVersions) != 1 || fake.create.SupportedOSVersions[0] != "Ubuntu 24" {
							return fmt.Errorf("create/get/key/input = %d/%d/%q/%#v", fake.createCalls, fake.getCalls, fake.idempotencyKey, fake.create)
						}
						return nil
					},
				),
			},
			{
				Config:   config,
				PlanOnly: true,
				Check: func(*terraform.State) error {
					fake.mu.Lock()
					defer fake.mu.Unlock()
					if fake.createCalls != 1 || fake.updateCalls != 0 {
						return fmt.Errorf("create/update calls = %d/%d, want 1/0", fake.createCalls, fake.updateCalls)
					}
					return nil
				},
			},
			{
				Config: testProviderConfig(fake) + componentResourceConfig("updated by provider test"),
				Check: testresource.ComposeTestCheckFunc(
					testresource.TestCheckResourceAttr("vew_component.test", "description", "updated by provider test"),
					testresource.TestCheckResourceAttr("vew_component.test", "updated_by", "terraform-test-updater"),
					func(*terraform.State) error {
						fake.mu.Lock()
						defer fake.mu.Unlock()
						if fake.updateCalls != 1 || fake.update.Description != "updated by provider test" || fake.getCalls < 2 {
							return fmt.Errorf("update/input/get = %d/%q/%d", fake.updateCalls, fake.update.Description, fake.getCalls)
						}
						return nil
					},
				),
			},
			{
				ResourceName:      "vew_component.test",
				ImportState:       true,
				ImportStateId:     "prog-73488/cmp-123",
				ImportStateVerify: true,
			},
		},
	})
}

func TestComponentResourceReadRemoves(t *testing.T) {
	for _, test := range []struct {
		name     string
		notFound bool
		status   string
	}{
		{name: "not found", notFound: true, status: "CREATED"},
		{name: "archived", status: "ARCHIVED"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := newFakeVEWServer(t)
			fake.component = testComponent(test.status)
			fake.notFound = test.notFound
			r := configuredComponentResource(t, fake)
			state := componentTestState(t, r, testComponent("CREATED"))
			response := resource.ReadResponse{State: state}
			r.Read(context.Background(), resource.ReadRequest{State: state}, &response)
			assertNoDiagnostics(t, response.Diagnostics)
			if !response.State.Raw.IsNull() {
				t.Fatalf("read state = %#v, want removed state", response.State.Raw)
			}
		})
	}
}

func TestComponentResourceCreateKeepsProvisionalStateWhenCanonicalReadFails(t *testing.T) {
	fake := newFakeVEWServer(t)
	fake.failGets = 4
	fake.failGetDetail = "upstream included " + testClientSecret
	r := configuredComponentResource(t, fake)
	plan := componentTestPlan(t, r)
	response := resource.CreateResponse{State: componentEmptyState(t, r)}

	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &response)
	assertNoDiagnostics(t, response.Diagnostics)
	if len(response.Diagnostics) != 1 || response.Diagnostics[0].Severity().String() != "Warning" {
		t.Fatalf("diagnostics = %s, want one warning", diagnosticsString(response.Diagnostics))
	}
	assertDiagnosticContains(t, response.Diagnostics, "cmp-123")
	assertDiagnosticContains(t, response.Diagnostics, "503")
	assertDiagnosticsOmit(t, response.Diagnostics, testClientSecret)

	var provisional componentModel
	assertNoDiagnostics(t, response.State.Get(context.Background(), &provisional))
	if provisional.ID.ValueString() != "cmp-123" || provisional.ProjectID.ValueString() != "prog-73488" || provisional.Description.ValueString() != "created by provider test" {
		t.Fatalf("provisional state = %#v", provisional)
	}
	for _, value := range []types.String{provisional.Status, provisional.CreatedAt, provisional.CreatedBy, provisional.UpdatedAt, provisional.UpdatedBy} {
		if !value.IsNull() {
			t.Fatalf("provisional computed value = %#v, want null", value)
		}
	}

	readResponse := resource.ReadResponse{State: response.State}
	r.Read(context.Background(), resource.ReadRequest{State: response.State}, &readResponse)
	assertNoDiagnostics(t, readResponse.Diagnostics)
	var canonical componentModel
	assertNoDiagnostics(t, readResponse.State.Get(context.Background(), &canonical))
	if canonical.Status.ValueString() != "CREATED" || canonical.CreatedBy.ValueString() != "terraform-test-user" {
		t.Fatalf("canonical state = %#v", canonical)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.createCalls != 1 {
		t.Fatalf("create calls = %d, want 1", fake.createCalls)
	}
}

func TestComponentResourceLifecycleRefreshesProvisionalCreateWithoutDuplicate(t *testing.T) {
	fake := newFakeVEWServer(t)
	fake.failGets = 4
	config := testProviderConfig(fake) + componentResourceConfig("created by provider test")

	testresource.Test(t, testresource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testProtoV6ProviderFactories(),
		CheckDestroy: func(*terraform.State) error {
			fake.mu.Lock()
			defer fake.mu.Unlock()
			if !fake.archived {
				return fmt.Errorf("component was not archived")
			}
			return nil
		},
		Steps: []testresource.TestStep{
			{
				Config: config,
				Check: testresource.ComposeTestCheckFunc(
					testresource.TestCheckResourceAttr("vew_component.test", "id", "cmp-123"),
					func(*terraform.State) error {
						fake.mu.Lock()
						defer fake.mu.Unlock()
						if fake.createCalls != 1 {
							return fmt.Errorf("create calls = %d, want 1", fake.createCalls)
						}
						return nil
					},
				),
			},
			{
				Config: config,
				Check: testresource.ComposeTestCheckFunc(
					testresource.TestCheckResourceAttr("vew_component.test", "status", "CREATED"),
					func(*terraform.State) error {
						fake.mu.Lock()
						defer fake.mu.Unlock()
						if fake.createCalls != 1 {
							return fmt.Errorf("create calls = %d, want 1", fake.createCalls)
						}
						return nil
					},
				),
			},
		},
	})
}

func TestFakeVEWServerRejectsExtraJSONFields(t *testing.T) {
	fake := newFakeVEWServer(t)
	for _, test := range []struct {
		name string
		path string
		body string
	}{
		{
			name: "create",
			path: "/projects/prog-73488/components",
			body: `{"componentName":"terraform-test-component","componentDescription":"created by provider test","componentPlatform":"Linux","componentSupportedArchitectures":["arm64","x86_64"],"componentSupportedOsVersions":["Ubuntu 24"],"forbidden":true}`,
		},
		{
			name: "update",
			path: "/projects/prog-73488/components/cmp-123",
			body: `{"componentDescription":"updated by provider test","forbidden":true}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodPost, fake.server.URL+test.path, bytes.NewBufferString(test.body))
			if err != nil {
				t.Fatal(err)
			}
			if test.name == "update" {
				request.Method = http.MethodPut
			}
			request.Header.Set("Authorization", "Bearer "+testAccessToken)
			request.Header.Set("Content-Type", "application/json")
			response, err := fake.server.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusBadRequest)
			}
		})
	}
}

func componentResourceConfig(description string) string {
	return fmt.Sprintf(`
resource "vew_component" "test" {
  project_id              = "prog-73488"
  name                    = "terraform-test-component"
  description             = %q
  platform                = "Linux"
  supported_architectures = ["arm64", "x86_64"]
  supported_os_versions   = ["Ubuntu 24"]
}
`, description)
}

func configuredComponentResource(t *testing.T, fake *fakeVEWServer) *componentResource {
	t.Helper()
	api, err := client.New(client.Config{APIURL: fake.server.URL, TokenURL: fake.server.URL + "/oauth/token", ClientID: testClientID, ClientSecret: testClientSecret})
	if err != nil {
		t.Fatal(err)
	}
	return &componentResource{client: api}
}

func componentTestState(t *testing.T, r *componentResource, component client.Component) tfsdk.State {
	t.Helper()
	var schemaResponse resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &schemaResponse)
	state := tfsdk.State{Schema: schemaResponse.Schema, Raw: tftypes.NewValue(schemaResponse.Schema.Type().TerraformType(context.Background()), nil)}
	model := componentModel{ProjectID: types.StringValue("prog-73488")}
	assertNoDiagnostics(t, setComponentState(context.Background(), &model, component))
	assertNoDiagnostics(t, state.Set(context.Background(), &model))
	return state
}

func componentTestPlan(t *testing.T, r *componentResource) tfsdk.Plan {
	t.Helper()
	var schemaResponse resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &schemaResponse)
	plan := tfsdk.Plan{Schema: schemaResponse.Schema, Raw: tftypes.NewValue(schemaResponse.Schema.Type().TerraformType(context.Background()), nil)}
	model := componentModel{
		ProjectID:              types.StringValue("prog-73488"),
		Name:                   types.StringValue("terraform-test-component"),
		Description:            types.StringValue("created by provider test"),
		Platform:               types.StringValue("Linux"),
		SupportedArchitectures: types.SetValueMust(types.StringType, []attr.Value{types.StringValue("arm64"), types.StringValue("x86_64")}),
		SupportedOSVersions:    types.SetValueMust(types.StringType, []attr.Value{types.StringValue("Ubuntu 24")}),
		ID:                     types.StringUnknown(),
		Status:                 types.StringUnknown(),
		CreatedAt:              types.StringUnknown(),
		CreatedBy:              types.StringUnknown(),
		UpdatedAt:              types.StringUnknown(),
		UpdatedBy:              types.StringUnknown(),
	}
	assertNoDiagnostics(t, plan.Set(context.Background(), &model))
	return plan
}

func componentEmptyState(t *testing.T, r *componentResource) tfsdk.State {
	t.Helper()
	var schemaResponse resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &schemaResponse)
	return tfsdk.State{Schema: schemaResponse.Schema, Raw: tftypes.NewValue(schemaResponse.Schema.Type().TerraformType(context.Background()), nil)}
}

func testComponent(status string) client.Component {
	return client.Component{
		ID: "cmp-123", Name: "terraform-test-component", Description: "created by provider test", Platform: "Linux",
		SupportedArchitectures: []string{"arm64", "x86_64"}, SupportedOSVersions: []string{"Ubuntu 24"},
		Status: status, CreatedAt: "2026-09-21T12:00:00Z", CreatedBy: "terraform-test-user",
		UpdatedAt: "2026-09-21T12:00:00Z", UpdatedBy: "terraform-test-user",
	}
}
