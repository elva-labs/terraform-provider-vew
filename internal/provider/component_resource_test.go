package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
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
