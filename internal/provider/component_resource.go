package provider

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/elva-labs/terraform-provider-vew/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// componentResource implements the VEW component resource schema and provider wiring.
type componentResource struct {
	client client.ComponentAPI
}

type componentModel struct {
	ID                     types.String `tfsdk:"id"`
	ProjectID              types.String `tfsdk:"project_id"`
	Name                   types.String `tfsdk:"name"`
	Description            types.String `tfsdk:"description"`
	Platform               types.String `tfsdk:"platform"`
	SupportedArchitectures types.Set    `tfsdk:"supported_architectures"`
	SupportedOSVersions    types.Set    `tfsdk:"supported_os_versions"`
	Status                 types.String `tfsdk:"status"`
	CreatedAt              types.String `tfsdk:"created_at"`
	CreatedBy              types.String `tfsdk:"created_by"`
	UpdatedAt              types.String `tfsdk:"updated_at"`
	UpdatedBy              types.String `tfsdk:"updated_by"`
}

// NewComponentResource constructs the vew_component resource.
func NewComponentResource() *componentResource {
	return &componentResource{}
}

func (r *componentResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_component"
}

func (r *componentResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = resourceschema.Schema{Attributes: map[string]resourceschema.Attribute{
		"id":                      resourceschema.StringAttribute{Computed: true},
		"project_id":              requiredReplacementString(),
		"name":                    requiredReplacementString(),
		"description":             resourceschema.StringAttribute{Required: true},
		"platform":                requiredReplacementString(),
		"supported_architectures": requiredReplacementSet(),
		"supported_os_versions":   requiredReplacementSet(),
		"status":                  resourceschema.StringAttribute{Computed: true},
		"created_at":              resourceschema.StringAttribute{Computed: true},
		"created_by":              resourceschema.StringAttribute{Computed: true},
		"updated_at":              resourceschema.StringAttribute{Computed: true},
		"updated_by":              resourceschema.StringAttribute{Computed: true},
	}}
}

func requiredReplacementString() resourceschema.StringAttribute {
	return resourceschema.StringAttribute{
		Required:      true,
		PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
	}
}

func requiredReplacementSet() resourceschema.SetAttribute {
	return resourceschema.SetAttribute{
		Required:      true,
		ElementType:   types.StringType,
		PlanModifiers: []planmodifier.Set{setplanmodifier.RequiresReplace()},
	}
}

func (r *componentResource) Configure(_ context.Context, request resource.ConfigureRequest, response *resource.ConfigureResponse) {
	if request.ProviderData == nil {
		return
	}
	api, ok := request.ProviderData.(client.ComponentAPI)
	if !ok {
		response.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			"Expected provider data to implement client.ComponentAPI.",
		)
		return
	}
	r.client = api
}

func (r *componentResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	projectID, componentID, err := parseComponentImportID(request.ID)
	if err != nil {
		response.Diagnostics.AddError("Invalid component import ID", err.Error())
		return
	}
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("project_id"), projectID)...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), componentID)...)
}

func (r *componentResource) Create(_ context.Context, _ resource.CreateRequest, response *resource.CreateResponse) {
	lifecycleNotImplemented(&response.Diagnostics)
}

func (r *componentResource) Read(_ context.Context, _ resource.ReadRequest, response *resource.ReadResponse) {
	lifecycleNotImplemented(&response.Diagnostics)
}

func (r *componentResource) Update(_ context.Context, _ resource.UpdateRequest, response *resource.UpdateResponse) {
	lifecycleNotImplemented(&response.Diagnostics)
}

func (r *componentResource) Delete(_ context.Context, _ resource.DeleteRequest, response *resource.DeleteResponse) {
	lifecycleNotImplemented(&response.Diagnostics)
}

func lifecycleNotImplemented(diagnostics *diag.Diagnostics) {
	diagnostics.AddError(
		"Component resource lifecycle not implemented",
		"The vew_component lifecycle is not available in this provider build.",
	)
}

func parseComponentImportID(value string) (projectID, componentID string, err error) {
	parts := strings.Split(value, "/")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", fmt.Errorf("expected import ID in project_id/component_id format")
	}
	return parts[0], parts[1], nil
}

func createInput(ctx context.Context, model componentModel) (client.CreateComponentInput, diag.Diagnostics) {
	architectures, diagnostics := setStrings(ctx, model.SupportedArchitectures, "supported_architectures")
	osVersions, osDiagnostics := setStrings(ctx, model.SupportedOSVersions, "supported_os_versions")
	diagnostics.Append(osDiagnostics...)
	if diagnostics.HasError() {
		return client.CreateComponentInput{}, diagnostics
	}
	return client.CreateComponentInput{
		Name:                   model.Name.ValueString(),
		Description:            model.Description.ValueString(),
		Platform:               model.Platform.ValueString(),
		SupportedArchitectures: architectures,
		SupportedOSVersions:    osVersions,
	}, diagnostics
}

func updateInput(model componentModel) client.UpdateComponentInput {
	return client.UpdateComponentInput{Description: model.Description.ValueString()}
}

func setComponentState(model *componentModel, component client.Component) {
	model.ID = types.StringValue(component.ID)
	model.Name = types.StringValue(component.Name)
	model.Description = types.StringValue(component.Description)
	model.Platform = types.StringValue(component.Platform)
	model.SupportedArchitectures = stringSet(component.SupportedArchitectures)
	model.SupportedOSVersions = stringSet(component.SupportedOSVersions)
	model.Status = types.StringValue(component.Status)
	model.CreatedAt = types.StringValue(component.CreatedAt)
	model.CreatedBy = types.StringValue(component.CreatedBy)
	model.UpdatedAt = types.StringValue(component.UpdatedAt)
	model.UpdatedBy = types.StringValue(component.UpdatedBy)
}

func setStrings(ctx context.Context, value types.Set, name string) ([]string, diag.Diagnostics) {
	var diagnostics diag.Diagnostics
	if value.IsNull() || value.IsUnknown() {
		diagnostics.AddError("Invalid component collection", name+" must be known and non-null.")
		return nil, diagnostics
	}
	var values []string
	diagnostics.Append(value.ElementsAs(ctx, &values, false)...)
	if diagnostics.HasError() {
		return nil, diagnostics
	}
	sort.Strings(values)
	return values, diagnostics
}

func stringSet(values []string) types.Set {
	values = append([]string(nil), values...)
	sort.Strings(values)
	elements := make([]attr.Value, len(values))
	for index, value := range values {
		elements[index] = types.StringValue(value)
	}
	return types.SetValueMust(types.StringType, elements)
}
