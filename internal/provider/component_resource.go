package provider

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/elva-labs/terraform-provider-vew/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
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

func (r *componentResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	var plan componentModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	input, diagnostics := createInput(ctx, plan)
	response.Diagnostics.Append(diagnostics...)
	if response.Diagnostics.HasError() {
		return
	}

	componentID, err := r.client.CreateComponent(ctx, plan.ProjectID.ValueString(), input)
	if err != nil {
		response.Diagnostics.AddError("Unable to create VEW component", "The VEW component could not be created.")
		return
	}
	component, err := r.client.GetComponent(ctx, plan.ProjectID.ValueString(), componentID)
	if err != nil {
		response.Diagnostics.AddError("Unable to read created VEW component", "The VEW component was created but its canonical state could not be read.")
		return
	}

	response.Diagnostics.Append(setComponentState(ctx, &plan, component)...)
	if response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (r *componentResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	var state componentModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	r.readComponent(ctx, &state, &response.State, &response.Diagnostics)
}

func (r *componentResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan, state componentModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	err := r.client.UpdateComponent(ctx, state.ProjectID.ValueString(), state.ID.ValueString(), updateInput(plan))
	if err != nil {
		response.Diagnostics.AddError("Unable to update VEW component", "The VEW component description could not be updated.")
		return
	}
	r.readComponent(ctx, &state, &response.State, &response.Diagnostics)
}

func (r *componentResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	var state componentModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	if err := r.client.ArchiveComponent(ctx, state.ProjectID.ValueString(), state.ID.ValueString()); err != nil {
		response.Diagnostics.AddError("Unable to archive VEW component", "The VEW component could not be archived.")
	}
}

func (r *componentResource) readComponent(ctx context.Context, state *componentModel, terraformState *tfsdk.State, diagnostics *diag.Diagnostics) {
	component, err := r.client.GetComponent(ctx, state.ProjectID.ValueString(), state.ID.ValueString())
	if client.IsNotFound(err) {
		terraformState.RemoveResource(ctx)
		return
	}
	if err != nil {
		diagnostics.AddError("Unable to read VEW component", "The VEW component state could not be read.")
		return
	}
	if strings.EqualFold(component.Status, "ARCHIVED") {
		terraformState.RemoveResource(ctx)
		return
	}

	diagnostics.Append(setComponentState(ctx, state, component)...)
	if diagnostics.HasError() {
		return
	}
	diagnostics.Append(terraformState.Set(ctx, state)...)
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

func setComponentState(ctx context.Context, model *componentModel, component client.Component) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	model.ID = types.StringValue(component.ID)
	model.Name = types.StringValue(component.Name)
	model.Description = types.StringValue(component.Description)
	model.Platform = types.StringValue(component.Platform)
	architectures, architectureDiagnostics := types.SetValueFrom(ctx, types.StringType, component.SupportedArchitectures)
	diagnostics.Append(architectureDiagnostics...)
	if !architectureDiagnostics.HasError() {
		model.SupportedArchitectures = architectures
	}
	osVersions, osVersionDiagnostics := types.SetValueFrom(ctx, types.StringType, component.SupportedOSVersions)
	diagnostics.Append(osVersionDiagnostics...)
	if !osVersionDiagnostics.HasError() {
		model.SupportedOSVersions = osVersions
	}
	model.Status = types.StringValue(component.Status)
	model.CreatedAt = types.StringValue(component.CreatedAt)
	model.CreatedBy = types.StringValue(component.CreatedBy)
	model.UpdatedAt = types.StringValue(component.UpdatedAt)
	model.UpdatedBy = types.StringValue(component.UpdatedBy)
	return diagnostics
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
