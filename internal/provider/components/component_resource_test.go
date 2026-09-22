package components

import (
	"context"
	"strings"
	"testing"

	"github.com/elva-labs/terraform-provider-vew/internal/providerdata"
	vewcomponents "github.com/elva-labs/terraform-provider-vew/internal/vew/components"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestComponentResourceSchema(t *testing.T) {
	r := NewComponentResource()
	var response resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &response)
	if len(response.Schema.Attributes) != 12 {
		t.Fatalf("attributes = %d, want 12", len(response.Schema.Attributes))
	}

	for _, name := range []string{"project_id", "name", "description", "platform"} {
		attribute, ok := response.Schema.Attributes[name].(resourceschema.StringAttribute)
		if !ok || !attribute.IsRequired() || attribute.IsOptional() || attribute.IsComputed() {
			t.Fatalf("%s schema = %#v, want required-only string", name, response.Schema.Attributes[name])
		}
	}
	for _, name := range []string{"id", "status", "created_at", "created_by", "updated_at", "updated_by"} {
		attribute, ok := response.Schema.Attributes[name].(resourceschema.StringAttribute)
		if !ok || !attribute.IsComputed() || attribute.IsOptional() || attribute.IsRequired() {
			t.Fatalf("%s schema = %#v, want computed-only string", name, response.Schema.Attributes[name])
		}
	}
	for _, name := range []string{"supported_architectures", "supported_os_versions"} {
		attribute, ok := response.Schema.Attributes[name].(resourceschema.SetAttribute)
		if !ok || !attribute.IsRequired() || attribute.IsOptional() || attribute.IsComputed() || attribute.ElementType != types.StringType {
			t.Fatalf("%s schema = %#v, want required-only set of strings", name, response.Schema.Attributes[name])
		}
		assertSetChangeRequiresReplacement(t, name, attribute)
	}
	for _, name := range []string{"project_id", "name", "platform"} {
		assertStringChangeRequiresReplacement(t, name, response.Schema.Attributes[name].(resourceschema.StringAttribute))
	}
}

func assertStringChangeRequiresReplacement(t *testing.T, name string, attribute resourceschema.StringAttribute) {
	t.Helper()
	if len(attribute.PlanModifiers) != 1 {
		t.Fatalf("%s replacement modifiers = %d, want 1", name, len(attribute.PlanModifiers))
	}
	response := planmodifier.StringResponse{}
	attribute.PlanModifiers[0].PlanModifyString(context.Background(), planmodifier.StringRequest{
		State:      tfsdk.State{Raw: tftypes.NewValue(tftypes.String, "prior")},
		Plan:       tfsdk.Plan{Raw: tftypes.NewValue(tftypes.String, "planned")},
		StateValue: types.StringValue("prior"),
		PlanValue:  types.StringValue("planned"),
	}, &response)
	if !response.RequiresReplace {
		t.Fatalf("%s change did not require replacement", name)
	}
}

func assertSetChangeRequiresReplacement(t *testing.T, name string, attribute resourceschema.SetAttribute) {
	t.Helper()
	if len(attribute.PlanModifiers) != 1 {
		t.Fatalf("%s replacement modifiers = %d, want 1", name, len(attribute.PlanModifiers))
	}
	response := planmodifier.SetResponse{}
	attribute.PlanModifiers[0].PlanModifySet(context.Background(), planmodifier.SetRequest{
		State:      tfsdk.State{Raw: tftypes.NewValue(tftypes.String, "prior")},
		Plan:       tfsdk.Plan{Raw: tftypes.NewValue(tftypes.String, "planned")},
		StateValue: types.SetValueMust(types.StringType, []attr.Value{types.StringValue("prior")}),
		PlanValue:  types.SetValueMust(types.StringType, []attr.Value{types.StringValue("planned")}),
	}, &response)
	if !response.RequiresReplace {
		t.Fatalf("%s change did not require replacement", name)
	}
}

func TestComponentResourceConfigureRequiresProviderData(t *testing.T) {
	r := NewComponentResource()
	var response resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: "wrong"}, &response)
	if !response.Diagnostics.HasError() || !strings.Contains(response.Diagnostics[0].Detail(), "providerdata.Data") {
		t.Fatalf("diagnostics = %v", response.Diagnostics)
	}
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: providerdata.Data{Components: componentAPIStub{}}}, &response)
	if r.client == nil {
		t.Fatal("component API was not configured")
	}
}

type componentAPIStub struct{}

func (componentAPIStub) CreateComponent(context.Context, string, vewcomponents.CreateComponentInput) (string, error) {
	return "", nil
}
func (componentAPIStub) GetComponent(context.Context, string, string) (vewcomponents.Component, error) {
	return vewcomponents.Component{}, nil
}
func (componentAPIStub) UpdateComponent(context.Context, string, string, vewcomponents.UpdateComponentInput) error {
	return nil
}
func (componentAPIStub) ArchiveComponent(context.Context, string, string) error { return nil }
