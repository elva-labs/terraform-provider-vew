package components

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/elva-labs/terraform-provider-vew/internal/providerdata"
	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	vewcomponents "github.com/elva-labs/terraform-provider-vew/internal/vew/components"
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
	client vewcomponents.API
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
	data, ok := request.ProviderData.(providerdata.Data)
	if !ok {
		response.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			"Expected provider data to be providerdata.Data.",
		)
		return
	}
	r.client = data.Components
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
		response.Diagnostics.AddError("Unable to create VEW component", componentAPIDiagnostic("create", err))
		return
	}
	component, err := r.client.GetComponent(ctx, plan.ProjectID.ValueString(), componentID)
	if err != nil {
		setProvisionalComponentState(&plan, componentID)
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
		if response.Diagnostics.HasError() {
			return
		}
		response.Diagnostics.AddWarning(
			"VEW component created with provisional state",
			fmt.Sprintf("Component %q was created, but canonical state could not be read: %s. The provider will refresh state during the next read.", componentID, componentAPIDiagnostic("read", err)),
		)
		return
	}

	response.Diagnostics.Append(setComponentState(ctx, &plan, component)...)
	if response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func setProvisionalComponentState(model *componentModel, componentID string) {
	model.ID = types.StringValue(componentID)
	model.Status = types.StringNull()
	model.CreatedAt = types.StringNull()
	model.CreatedBy = types.StringNull()
	model.UpdatedAt = types.StringNull()
	model.UpdatedBy = types.StringNull()
}

func componentAPIDiagnostic(operation string, err error) string {
	message := fmt.Sprintf("VEW API %s failed", operation)
	var apiError *vew.APIError
	if !errors.As(err, &apiError) {
		return message
	}
	parts := []string{fmt.Sprintf("HTTP status %d", apiError.Status)}
	if code := strings.TrimSpace(apiError.Problem.Code); code != "" {
		parts = append(parts, "problem code "+code)
	}
	if requestID := strings.TrimSpace(apiError.Problem.RequestID); requestID != "" {
		parts = append(parts, "request ID "+requestID)
	}
	return message + " (" + strings.Join(parts, ", ") + ")"
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
		response.Diagnostics.AddError("Unable to update VEW component", componentAPIDiagnostic("update", err))
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
		response.Diagnostics.AddError("Unable to archive VEW component", componentAPIDiagnostic("archive", err))
	}
}

func (r *componentResource) readComponent(ctx context.Context, state *componentModel, terraformState *tfsdk.State, diagnostics *diag.Diagnostics) {
	component, err := r.client.GetComponent(ctx, state.ProjectID.ValueString(), state.ID.ValueString())
	if vew.IsNotFound(err) {
		terraformState.RemoveResource(ctx)
		return
	}
	if err != nil {
		diagnostics.AddError("Unable to read VEW component", componentAPIDiagnostic("read", err))
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

func createInput(ctx context.Context, model componentModel) (vewcomponents.CreateComponentInput, diag.Diagnostics) {
	architectures, diagnostics := setStrings(ctx, model.SupportedArchitectures, "supported_architectures")
	osVersions, osDiagnostics := setStrings(ctx, model.SupportedOSVersions, "supported_os_versions")
	diagnostics.Append(osDiagnostics...)
	if diagnostics.HasError() {
		return vewcomponents.CreateComponentInput{}, diagnostics
	}
	return vewcomponents.CreateComponentInput{
		Name:                   model.Name.ValueString(),
		Description:            model.Description.ValueString(),
		Platform:               model.Platform.ValueString(),
		SupportedArchitectures: architectures,
		SupportedOSVersions:    osVersions,
	}, diagnostics
}

func updateInput(model componentModel) vewcomponents.UpdateComponentInput {
	return vewcomponents.UpdateComponentInput{Description: model.Description.ValueString()}
}

func setComponentState(ctx context.Context, model *componentModel, component vewcomponents.Component) diag.Diagnostics {
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
