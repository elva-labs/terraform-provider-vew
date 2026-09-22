package components

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
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
	// These local copies are only used to decide replacement. Keep configured
	// representations in the actual plan when their normalized meanings match.
	stateDefinition, stateDefinitionErr := normalizeDefinition(state.DefinitionJSON.ValueString())
	planDefinition, planDefinitionErr := normalizeDefinition(plan.DefinitionJSON.ValueString())
	if stateDefinitionErr == nil && planDefinitionErr == nil && string(stateDefinition) == string(planDefinition) {
		plan.DefinitionJSON = state.DefinitionJSON
	}
	stateDependencies, stateDependenciesDiagnostics := expandDependencies(ctx, state.Dependencies)
	planDependencies, planDependenciesDiagnostics := expandDependencies(ctx, plan.Dependencies)
	if !stateDependenciesDiagnostics.HasError() && !planDependenciesDiagnostics.HasError() && reflect.DeepEqual(stateDependencies, planDependencies) {
		plan.Dependencies = state.Dependencies
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

func (r *componentVersionResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	var model componentVersionModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &model)...)
	if response.Diagnostics.HasError() {
		return
	}
	input, diagnostics := versionMutableInput(ctx, model)
	response.Diagnostics.Append(diagnostics...)
	if response.Diagnostics.HasError() {
		return
	}
	action, err := r.client.CreateComponentVersion(ctx, model.ProjectID.ValueString(), model.ComponentID.ValueString(), vewcomponents.CreateComponentVersionInput{
		Description: input.Description, Definition: input.Definition, Dependencies: input.Dependencies,
		ReleaseType: model.ReleaseType.ValueString(), SoftwareVendor: input.SoftwareVendor, SoftwareVersion: input.SoftwareVersion,
		LicenseDashboard: input.LicenseDashboard, Notes: input.Notes,
	})
	if err != nil {
		addVersionError(&response.Diagnostics, "create", err)
		return
	}
	model.ID = types.StringValue(action.ID)
	model.Status = types.StringValue("CREATING")
	model.Name, model.CreatedAt, model.CreatedBy, model.UpdatedAt, model.UpdatedBy = types.StringNull(), types.StringNull(), types.StringNull(), types.StringNull(), types.StringNull()
	// A cancellation must not prevent serialization of an accepted remote ID.
	stateCtx := context.WithoutCancel(ctx)
	response.Diagnostics.Append(response.State.Set(stateCtx, &model)...)
	if response.Diagnostics.HasError() {
		return
	}
	err = r.waitVersion(ctx, &model, operationTimeout(ctx, model.Timeouts, "create"), action.RetryAfter, validatedVersionStatus, false)
	response.Diagnostics.Append(response.State.Set(stateCtx, &model)...)
	if err != nil {
		addVersionError(&response.Diagnostics, "create", err)
	}
}

func versionMutableInput(ctx context.Context, model componentVersionModel) (vewcomponents.UpdateComponentVersionInput, diag.Diagnostics) {
	var diagnostics diag.Diagnostics
	definition, err := normalizeDefinition(model.DefinitionJSON.ValueString())
	if err != nil {
		diagnostics.AddError("Invalid component version definition", "The definition must be a single JSON object.")
	}
	dependencies, d := expandDependencies(ctx, model.Dependencies)
	diagnostics.Append(d...)
	return vewcomponents.UpdateComponentVersionInput{
		Description: model.Description.ValueString(), Definition: definition, Dependencies: dependencies,
		SoftwareVendor: model.SoftwareVendor.ValueString(), SoftwareVersion: model.SoftwareVersion.ValueString(),
		LicenseDashboard: model.LicenseDashboard.ValueStringPointer(), Notes: model.Notes.ValueStringPointer(),
	}, diagnostics
}

func setVersionState(ctx context.Context, model *componentVersionModel, version vewcomponents.ComponentVersion) error {
	// Status and identity remain recoverable even when another remote field is invalid.
	model.ID, model.Status = types.StringValue(version.ID), types.StringValue(version.Status)
	model.ComponentID = types.StringValue(version.ComponentID)
	model.Name, model.Description = types.StringValue(version.Name), types.StringValue(version.Description)
	model.SoftwareVendor, model.SoftwareVersion = types.StringValue(version.SoftwareVendor), types.StringValue(version.SoftwareVersion)
	model.LicenseDashboard, model.Notes = types.StringPointerValue(version.LicenseDashboard), types.StringPointerValue(version.Notes)
	model.CreatedAt, model.CreatedBy = types.StringValue(version.CreatedAt), types.StringValue(version.CreatedBy)
	model.UpdatedAt, model.UpdatedBy = types.StringValue(version.UpdatedAt), types.StringValue(version.UpdatedBy)
	definition, err := normalizeDefinition(string(version.Definition))
	if err != nil {
		return err
	}
	previousDefinition, previousErr := normalizeDefinition(model.DefinitionJSON.ValueString())
	if previousErr != nil || string(previousDefinition) != string(definition) {
		model.DefinitionJSON = types.StringValue(string(definition))
	}
	dependencies := make([]dependencyModel, 0, len(version.Dependencies))
	for _, dependency := range version.Dependencies {
		dependencies = append(dependencies, dependencyModel{
			ComponentID: types.StringValue(dependency.ComponentID), ComponentName: types.StringValue(dependency.ComponentName),
			VersionID: types.StringValue(dependency.VersionID), VersionName: types.StringValue(dependency.VersionName),
			Type: types.StringValue(dependency.Type), Order: types.Int64Value(dependency.Order), Position: types.StringPointerValue(dependency.Position),
		})
	}
	sort.SliceStable(dependencies, func(i, j int) bool { return dependencies[i].Order.ValueInt64() < dependencies[j].Order.ValueInt64() })
	value, diagnostics := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: componentVersionDependencyAttributeTypes}, dependencies)
	if diagnostics.HasError() {
		return errors.New("invalid component version dependencies")
	}
	previousDependencies, previousDiagnostics := expandDependencies(ctx, model.Dependencies)
	remoteDependencies, remoteDiagnostics := expandDependencies(ctx, value)
	if model.Dependencies.IsNull() || model.Dependencies.IsUnknown() || previousDiagnostics.HasError() || remoteDiagnostics.HasError() || !reflect.DeepEqual(previousDependencies, remoteDependencies) {
		model.Dependencies = value
	}
	return nil
}

func (r *componentVersionResource) getVersion(ctx context.Context, model *componentVersionModel) (vewcomponents.ComponentVersion, error) {
	return r.client.GetComponentVersion(ctx, model.ProjectID.ValueString(), model.ComponentID.ValueString(), model.ID.ValueString())
}

func (r *componentVersionResource) waitVersion(ctx context.Context, model *componentVersionModel, timeout, delay time.Duration, evaluate vew.StatusEvaluator, missingIsRetired bool) error {
	// Retain the last successfully read representation even if a later poll fails.
	definition, dependencies := model.DefinitionJSON, model.Dependencies
	return r.waiter.Until(ctx, timeout, delay, func(ctx context.Context) (vew.PollResult, error) {
		version, err := r.getVersion(ctx, model)
		if missingIsRetired && vew.IsNotFound(err) {
			model.Status = types.StringValue("RETIRED")
			return vew.PollResult{Status: "RETIRED"}, nil
		}
		if err != nil {
			return vew.PollResult{}, err
		}
		candidate := *model
		candidate.DefinitionJSON, candidate.Dependencies = definition, dependencies
		if err := setVersionState(context.WithoutCancel(ctx), &candidate, version); err != nil {
			return vew.PollResult{}, err
		}
		*model = candidate
		return vew.PollResult{Status: version.Status}, nil
	}, evaluate)
}

func pendingVersionStatus(status string) bool {
	return status == "CREATING" || status == "CREATED" || status == "TESTING" || status == "UPDATING"
}

func validatedVersionStatus(status string) (bool, error) {
	if pendingVersionStatus(status) {
		return false, nil
	}
	switch status {
	case "VALIDATED":
		return true, nil
	case "FAILED", "RELEASED", "RETIRED":
		return false, &vew.TerminalStatusError{Status: status}
	default:
		return false, errors.New("unrecognized component version status")
	}
}

func addVersionError(diagnostics *diag.Diagnostics, operation string, err error) {
	message := "VEW component version " + operation + " failed. Refresh state and retry the operation."
	var terminal *vew.TerminalStatusError
	var timeout *vew.TimeoutError
	var api *vew.APIError
	switch {
	case errors.As(err, &terminal):
		// Only statuses from the explicit state machine enter this error type.
		message = "VEW component version reached " + terminal.Status + ". Refresh state and retry the operation."
	case errors.As(err, &timeout):
		message = "Timed out waiting for VEW component version " + operation + ". The latest recoverable state has been retained."
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		message = "VEW component version " + operation + " was cancelled. The latest recoverable state has been retained."
	case errors.As(err, &api):
		message = fmt.Sprintf("VEW component version %s failed (HTTP status %d).", operation, api.Status)
	}
	diagnostics.AddError("Unable to "+operation+" VEW component version", message)
}

func (r *componentVersionResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	var model componentVersionModel
	response.Diagnostics.Append(request.State.Get(ctx, &model)...)
	if response.Diagnostics.HasError() {
		return
	}
	version, err := r.getVersion(ctx, &model)
	if vew.IsNotFound(err) || (err == nil && version.Status == "RETIRED") {
		response.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		addVersionError(&response.Diagnostics, "read", err)
		return
	}
	err = setVersionState(ctx, &model, version)
	response.Diagnostics.Append(response.State.Set(context.WithoutCancel(ctx), &model)...)
	if err != nil {
		addVersionError(&response.Diagnostics, "read", err)
	}
}

func (r *componentVersionResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var model, plan componentVersionModel
	response.Diagnostics.Append(request.State.Get(ctx, &model)...)
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	input, diagnostics := versionMutableInput(ctx, plan)
	response.Diagnostics.Append(diagnostics...)
	if response.Diagnostics.HasError() {
		return
	}
	adopting := (model.ReleaseType.IsNull() || model.ReleaseType.IsUnknown()) && !plan.ReleaseType.IsNull() && !plan.ReleaseType.IsUnknown() && equivalentVersionConfiguration(ctx, model, plan)
	model.ReleaseType, model.Timeouts = plan.ReleaseType, plan.Timeouts
	stateCtx := context.WithoutCancel(ctx)
	timeout := operationTimeout(ctx, plan.Timeouts, "update")
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// Always save the latest observation before reporting an operation failure.
	defer func() { response.Diagnostics.Append(response.State.Set(stateCtx, &model)...) }()
	version, err := r.getVersion(ctx, &model)
	if err != nil {
		addVersionError(&response.Diagnostics, "update", err)
		return
	}
	if err = setVersionState(stateCtx, &model, version); err != nil {
		addVersionError(&response.Diagnostics, "update", err)
		return
	}
	if pendingVersionStatus(model.Status.ValueString()) {
		if err = r.waitVersion(ctx, &model, timeout, 0, validatedVersionStatus, false); err != nil {
			addVersionError(&response.Diagnostics, "update", err)
			return
		}
	}
	if adopting && equivalentVersionConfiguration(ctx, model, plan) {
		model.DefinitionJSON, model.Dependencies = plan.DefinitionJSON, plan.Dependencies
		return
	}
	if model.Status.ValueString() != "VALIDATED" && model.Status.ValueString() != "FAILED" {
		addVersionError(&response.Diagnostics, "update", errors.New("version cannot be updated in its current status"))
		return
	}
	action, err := r.client.UpdateComponentVersion(ctx, model.ProjectID.ValueString(), model.ComponentID.ValueString(), model.ID.ValueString(), input)
	if err != nil {
		addVersionError(&response.Diagnostics, "update", err)
		return
	}
	model.DefinitionJSON, model.Dependencies = plan.DefinitionJSON, plan.Dependencies
	err = r.waitVersion(ctx, &model, timeout, action.RetryAfter, validatedVersionStatus, false)
	if err != nil {
		addVersionError(&response.Diagnostics, "update", err)
	}
}

func equivalentVersionConfiguration(ctx context.Context, left, right componentVersionModel) bool {
	leftInput, leftDiagnostics := versionMutableInput(ctx, left)
	rightInput, rightDiagnostics := versionMutableInput(ctx, right)
	return !leftDiagnostics.HasError() && !rightDiagnostics.HasError() && reflect.DeepEqual(leftInput, rightInput)
}

func (r *componentVersionResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	var model componentVersionModel
	response.Diagnostics.Append(request.State.Get(ctx, &model)...)
	if response.Diagnostics.HasError() {
		return
	}
	stateCtx := context.WithoutCancel(ctx)
	timeout := operationTimeout(ctx, model.Timeouts, "delete")
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	removed := false
	defer func() {
		if removed {
			response.State.RemoveResource(stateCtx)
		} else {
			response.Diagnostics.Append(response.State.Set(stateCtx, &model)...)
		}
	}()
	version, err := r.getVersion(ctx, &model)
	if vew.IsNotFound(err) || (err == nil && version.Status == "RETIRED") {
		removed = true
		return
	}
	if err != nil {
		addVersionError(&response.Diagnostics, "delete", err)
		return
	}
	if err = setVersionState(stateCtx, &model, version); err != nil {
		addVersionError(&response.Diagnostics, "delete", err)
		return
	}
	if pendingVersionStatus(model.Status.ValueString()) {
		if err = r.waitVersion(ctx, &model, timeout, 0, deletableVersionStatus, true); err != nil {
			addVersionError(&response.Diagnostics, "delete", err)
			return
		}
	}
	if model.Status.ValueString() == "RETIRED" {
		removed = true
		return
	}
	if _, err = deletableVersionStatus(model.Status.ValueString()); err != nil {
		addVersionError(&response.Diagnostics, "delete", err)
		return
	}
	action, err := r.client.RetireComponentVersion(ctx, model.ProjectID.ValueString(), model.ComponentID.ValueString(), model.ID.ValueString())
	if vew.IsNotFound(err) {
		removed = true
		return
	}
	if err != nil {
		addVersionError(&response.Diagnostics, "delete", err)
		return
	}
	err = r.waitVersion(ctx, &model, timeout, action.RetryAfter, retiredVersionStatus, true)
	if err != nil {
		addVersionError(&response.Diagnostics, "delete", err)
		return
	}
	removed = true
}

func deletableVersionStatus(status string) (bool, error) {
	if pendingVersionStatus(status) {
		return false, nil
	}
	switch status {
	case "VALIDATED", "FAILED", "RELEASED", "RETIRED":
		return true, nil
	default:
		return false, errors.New("unrecognized component version status")
	}
}

func retiredVersionStatus(status string) (bool, error) {
	if pendingVersionStatus(status) || status == "VALIDATED" || status == "RELEASED" {
		return false, nil
	}
	switch status {
	case "RETIRED":
		return true, nil
	case "FAILED":
		return false, &vew.TerminalStatusError{Status: status}
	default:
		return false, errors.New("unrecognized component version status")
	}
}
