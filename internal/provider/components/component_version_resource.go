package components

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/elva-labs/terraform-provider-vew/internal/providerdata"
	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	vewcomponents "github.com/elva-labs/terraform-provider-vew/internal/vew/components"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

var (
	_ resource.Resource                   = &componentVersionResource{}
	_ resource.ResourceWithConfigure      = &componentVersionResource{}
	_ resource.ResourceWithImportState    = &componentVersionResource{}
	_ resource.ResourceWithValidateConfig = &componentVersionResource{}
	_ resource.ResourceWithModifyPlan     = &componentVersionResource{}
)

var componentVersionDependencyAttributeTypes = map[string]attr.Type{
	"component_id":   types.StringType,
	"component_name": types.StringType,
	"version_id":     types.StringType,
	"version_name":   types.StringType,
	"type":           types.StringType,
	"order":          types.Int64Type,
	"position":       types.StringType,
}

var timeoutAttributeTypes = map[string]attr.Type{
	"create": types.StringType,
	"update": types.StringType,
	"delete": types.StringType,
}

type componentVersionResource struct {
	client vewcomponents.ComponentVersionAPI
	waiter vew.Waiter
}

type componentVersionModel struct {
	ID               types.String `tfsdk:"id"`
	ProjectID        types.String `tfsdk:"project_id"`
	ComponentID      types.String `tfsdk:"component_id"`
	Description      types.String `tfsdk:"description"`
	ReleaseType      types.String `tfsdk:"release_type"`
	DefinitionJSON   types.String `tfsdk:"definition_json"`
	Dependencies     types.List   `tfsdk:"dependencies"`
	SoftwareVendor   types.String `tfsdk:"software_vendor"`
	SoftwareVersion  types.String `tfsdk:"software_version"`
	LicenseDashboard types.String `tfsdk:"license_dashboard"`
	Notes            types.String `tfsdk:"notes"`
	Name             types.String `tfsdk:"name"`
	Status           types.String `tfsdk:"status"`
	CreatedAt        types.String `tfsdk:"created_at"`
	CreatedBy        types.String `tfsdk:"created_by"`
	UpdatedAt        types.String `tfsdk:"updated_at"`
	UpdatedBy        types.String `tfsdk:"updated_by"`
	Timeouts         types.Object `tfsdk:"timeouts"`
}

type timeoutsModel struct {
	Create types.String `tfsdk:"create"`
	Update types.String `tfsdk:"update"`
	Delete types.String `tfsdk:"delete"`
}

// NewComponentVersionResource constructs the vew_component_version resource.
func NewComponentVersionResource() resource.Resource {
	return &componentVersionResource{}
}

func (r *componentVersionResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_component_version"
}

func (r *componentVersionResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = resourceschema.Schema{
		Attributes: map[string]resourceschema.Attribute{
			"id":                resourceschema.StringAttribute{Computed: true},
			"project_id":        requiredReplacementString(),
			"component_id":      requiredReplacementString(),
			"description":       resourceschema.StringAttribute{Required: true},
			"release_type":      resourceschema.StringAttribute{Required: true},
			"definition_json":   resourceschema.StringAttribute{Required: true},
			"dependencies":      componentVersionDependenciesAttribute(),
			"software_vendor":   resourceschema.StringAttribute{Required: true},
			"software_version":  resourceschema.StringAttribute{Required: true},
			"license_dashboard": resourceschema.StringAttribute{Optional: true},
			"notes":             resourceschema.StringAttribute{Optional: true},
			"name":              resourceschema.StringAttribute{Computed: true},
			"status":            resourceschema.StringAttribute{Computed: true},
			"created_at":        resourceschema.StringAttribute{Computed: true},
			"created_by":        resourceschema.StringAttribute{Computed: true},
			"updated_at":        resourceschema.StringAttribute{Computed: true},
			"updated_by":        resourceschema.StringAttribute{Computed: true},
		},
		Blocks: map[string]resourceschema.Block{
			"timeouts": resourceschema.SingleNestedBlock{Attributes: map[string]resourceschema.Attribute{
				"create": resourceschema.StringAttribute{Optional: true},
				"update": resourceschema.StringAttribute{Optional: true},
				"delete": resourceschema.StringAttribute{Optional: true},
			}},
		},
	}
}

func componentVersionDependenciesAttribute() resourceschema.ListNestedAttribute {
	return resourceschema.ListNestedAttribute{
		Optional: true,
		Computed: true,
		Default:  listdefault.StaticValue(types.ListValueMust(types.ObjectType{AttrTypes: componentVersionDependencyAttributeTypes}, nil)),
		NestedObject: resourceschema.NestedAttributeObject{Attributes: map[string]resourceschema.Attribute{
			"component_id":   resourceschema.StringAttribute{Required: true},
			"component_name": resourceschema.StringAttribute{Required: true},
			"version_id":     resourceschema.StringAttribute{Required: true},
			"version_name":   resourceschema.StringAttribute{Required: true},
			"type":           resourceschema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("HELPER")},
			"order":          resourceschema.Int64Attribute{Required: true},
			"position":       resourceschema.StringAttribute{Optional: true},
		}},
	}
}

func (r *componentVersionResource) Configure(_ context.Context, request resource.ConfigureRequest, response *resource.ConfigureResponse) {
	if request.ProviderData == nil {
		return
	}
	data, ok := request.ProviderData.(providerdata.Data)
	if !ok {
		response.Diagnostics.AddError("Unexpected Resource Configure Type", "Expected provider data to be providerdata.Data.")
		return
	}
	if data.ComponentVersions == nil {
		response.Diagnostics.AddError("Missing Component Version API", "Expected provider data to include a ComponentVersions API.")
		return
	}
	if data.Waiter == nil {
		response.Diagnostics.AddError("Missing VEW Waiter", "Expected provider data to include a Waiter.")
		return
	}
	r.client = data.ComponentVersions
	r.waiter = data.Waiter
}

func (r *componentVersionResource) ValidateConfig(ctx context.Context, request resource.ValidateConfigRequest, response *resource.ValidateConfigResponse) {
	var config componentVersionModel
	response.Diagnostics.Append(request.Config.Get(ctx, &config)...)
	if response.Diagnostics.HasError() {
		return
	}

	if !config.ReleaseType.IsUnknown() && !config.ReleaseType.IsNull() {
		releaseType := config.ReleaseType.ValueString()
		if releaseType != "MAJOR" && releaseType != "MINOR" && releaseType != "PATCH" {
			response.Diagnostics.AddAttributeError(path.Root("release_type"), "Invalid component version release type", "release_type must be MAJOR, MINOR, or PATCH.")
		}
	}
	if !config.DefinitionJSON.IsUnknown() && !config.DefinitionJSON.IsNull() {
		if _, err := normalizeDefinition(config.DefinitionJSON.ValueString()); err != nil {
			response.Diagnostics.AddAttributeError(path.Root("definition_json"), "Invalid component version definition", "definition_json must be a single JSON object.")
		}
	}
	if !config.Dependencies.IsUnknown() && !config.Dependencies.IsNull() {
		response.Diagnostics.Append(validateDependencies(ctx, config.Dependencies)...)
	}
	response.Diagnostics.Append(validateTimeouts(ctx, config.Timeouts)...)
}

func validateTimeouts(ctx context.Context, timeouts types.Object) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	if timeouts.IsNull() || timeouts.IsUnknown() {
		return diagnostics
	}
	var values timeoutsModel
	diagnostics.Append(timeouts.As(ctx, &values, basetypes.ObjectAsOptions{})...)
	if diagnostics.HasError() {
		return diagnostics
	}
	for _, timeout := range []struct {
		name  string
		value types.String
	}{
		{"create", values.Create},
		{"update", values.Update},
		{"delete", values.Delete},
	} {
		if timeout.value.IsNull() || timeout.value.IsUnknown() {
			continue
		}
		duration, err := time.ParseDuration(timeout.value.ValueString())
		if err != nil {
			diagnostics.AddAttributeError(path.Root("timeouts").AtName(timeout.name), "Invalid component version timeout", fmt.Sprintf("timeouts.%s must be a Go duration string.", timeout.name))
			continue
		}
		if duration <= 0 {
			diagnostics.AddAttributeError(path.Root("timeouts").AtName(timeout.name), "Invalid component version timeout", fmt.Sprintf("timeouts.%s must be greater than zero.", timeout.name))
		}
	}
	return diagnostics
}

func operationTimeout(ctx context.Context, timeouts types.Object, operation string) time.Duration {
	defaults := map[string]time.Duration{
		"create": time.Hour,
		"update": time.Hour,
		"delete": 30 * time.Minute,
	}
	defaultTimeout, ok := defaults[operation]
	if !ok || timeouts.IsNull() || timeouts.IsUnknown() {
		return defaultTimeout
	}
	var values timeoutsModel
	if diagnostics := timeouts.As(ctx, &values, basetypes.ObjectAsOptions{}); diagnostics.HasError() {
		return defaultTimeout
	}
	value := map[string]types.String{"create": values.Create, "update": values.Update, "delete": values.Delete}[operation]
	if value.IsNull() || value.IsUnknown() {
		return defaultTimeout
	}
	duration, err := time.ParseDuration(value.ValueString())
	if err != nil || duration <= 0 {
		return defaultTimeout
	}
	return duration
}

func (r *componentVersionResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	projectID, componentID, versionID, err := parseComponentVersionImportID(request.ID)
	if err != nil {
		response.Diagnostics.AddError("Invalid component version import ID", err.Error())
		return
	}
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("project_id"), projectID)...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("component_id"), componentID)...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), versionID)...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("release_type"), types.StringNull())...)
}

func parseComponentVersionImportID(value string) (projectID, componentID, versionID string, err error) {
	parts := strings.Split(value, "/")
	if len(parts) != 3 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" || strings.TrimSpace(parts[2]) == "" {
		return "", "", "", fmt.Errorf("expected import ID in project_id/component_id/version_id format")
	}
	return parts[0], parts[1], parts[2], nil
}

func (r *componentVersionResource) ModifyPlan(ctx context.Context, request resource.ModifyPlanRequest, response *resource.ModifyPlanResponse) {
	if request.Plan.Raw.IsNull() || request.State.Raw.IsNull() {
		return
	}
	var state, plan componentVersionModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	if !state.ReleaseType.IsUnknown() && !state.ReleaseType.IsNull() && !plan.ReleaseType.IsUnknown() && !plan.ReleaseType.IsNull() && !state.ReleaseType.Equal(plan.ReleaseType) {
		response.RequiresReplace = append(response.RequiresReplace, path.Root("release_type"))
	}
	if state.Status.IsUnknown() || state.Status.IsNull() || state.Status.ValueString() != "RELEASED" {
		return
	}
	for _, attribute := range []struct {
		path  path.Path
		state attr.Value
		plan  attr.Value
	}{
		{path.Root("description"), state.Description, plan.Description},
		{path.Root("definition_json"), state.DefinitionJSON, plan.DefinitionJSON},
		{path.Root("dependencies"), state.Dependencies, plan.Dependencies},
		{path.Root("software_vendor"), state.SoftwareVendor, plan.SoftwareVendor},
		{path.Root("software_version"), state.SoftwareVersion, plan.SoftwareVersion},
		{path.Root("license_dashboard"), state.LicenseDashboard, plan.LicenseDashboard},
		{path.Root("notes"), state.Notes, plan.Notes},
	} {
		if attribute.state.IsUnknown() || attribute.plan.IsUnknown() {
			continue
		}
		if !attribute.state.Equal(attribute.plan) {
			response.RequiresReplace = append(response.RequiresReplace, attribute.path)
		}
	}
}

func (r *componentVersionResource) Create(_ context.Context, _ resource.CreateRequest, response *resource.CreateResponse) {
	response.Diagnostics.AddError("Component version create not implemented", "The component version lifecycle will be available in a subsequent provider release.")
}

func (r *componentVersionResource) Read(_ context.Context, _ resource.ReadRequest, response *resource.ReadResponse) {
	response.Diagnostics.AddError("Component version read not implemented", "The component version lifecycle will be available in a subsequent provider release.")
}

func (r *componentVersionResource) Update(_ context.Context, _ resource.UpdateRequest, response *resource.UpdateResponse) {
	response.Diagnostics.AddError("Component version update not implemented", "The component version lifecycle will be available in a subsequent provider release.")
}

func (r *componentVersionResource) Delete(_ context.Context, _ resource.DeleteRequest, response *resource.DeleteResponse) {
	response.Diagnostics.AddError("Component version delete not implemented", "The component version lifecycle will be available in a subsequent provider release.")
}
