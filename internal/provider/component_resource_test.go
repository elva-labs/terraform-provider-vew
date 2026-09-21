package provider

import (
	"context"
	"fmt"
	"testing"

	"github.com/elva-labs/terraform-provider-vew/internal/client"
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
					testresource.TestCheckResourceAttr("vew_component.test", "status", "ACTIVE"),
					testresource.TestCheckResourceAttr("vew_component.test", "created_by", "terraform-test-user"),
					func(*terraform.State) error {
						fake.mu.Lock()
						defer fake.mu.Unlock()
						if fake.createCalls != 1 || fake.getCalls != 1 || fake.idempotencyKey == "" {
							return fmt.Errorf("create/get/key = %d/%d/%q", fake.createCalls, fake.getCalls, fake.idempotencyKey)
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
		{name: "not found", notFound: true, status: "ACTIVE"},
		{name: "archived", status: "ARCHIVED"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := newFakeVEWServer(t)
			fake.component = testComponent(test.status)
			fake.notFound = test.notFound
			r := configuredComponentResource(t, fake)
			state := componentTestState(t, r, testComponent("ACTIVE"))
			response := resource.ReadResponse{State: state}
			r.Read(context.Background(), resource.ReadRequest{State: state}, &response)
			assertNoDiagnostics(t, response.Diagnostics)
			if !response.State.Raw.IsNull() {
				t.Fatalf("read state = %#v, want removed state", response.State.Raw)
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

func testComponent(status string) client.Component {
	return client.Component{
		ID: "cmp-123", Name: "terraform-test-component", Description: "created by provider test", Platform: "Linux",
		SupportedArchitectures: []string{"arm64", "x86_64"}, SupportedOSVersions: []string{"Ubuntu 24"},
		Status: status, CreatedAt: "2026-09-21T12:00:00Z", CreatedBy: "terraform-test-user",
		UpdatedAt: "2026-09-21T12:00:00Z", UpdatedBy: "terraform-test-user",
	}
}
