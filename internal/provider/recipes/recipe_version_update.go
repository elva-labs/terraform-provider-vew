package recipes

import (
	"context"
	"errors"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const recipeVersionUpdateRetryKey = "recipe_version_update_retry"

func preserveRecipeVersionMutableConfiguration(model *recipeVersionModel, prior recipeVersionModel) {
	model.Description = prior.Description
	model.VolumeSize = prior.VolumeSize
	model.Integrations = prior.Integrations
	model.ConfiguredComponents = prior.ConfiguredComponents
}

func mergeRecipeComponentPlan(ctx context.Context, current, planned types.List) (types.List, diag.Diagnostics) {
	if planned.IsNull() || planned.IsUnknown() || current.IsNull() || current.IsUnknown() {
		return current, nil
	}
	var currentComponents, plannedComponents []recipeComponentModel
	var diagnostics diag.Diagnostics
	diagnostics.Append(current.ElementsAs(ctx, &currentComponents, false)...)
	diagnostics.Append(planned.ElementsAs(ctx, &plannedComponents, false)...)
	if diagnostics.HasError() {
		return current, diagnostics
	}
	if len(currentComponents) != len(plannedComponents) {
		return current, nil
	}
	for index := range plannedComponents {
		if plannedComponents[index].ComponentName.IsNull() || plannedComponents[index].ComponentName.IsUnknown() {
			plannedComponents[index].ComponentName = currentComponents[index].ComponentName
		}
		if plannedComponents[index].VersionName.IsNull() || plannedComponents[index].VersionName.IsUnknown() {
			plannedComponents[index].VersionName = currentComponents[index].VersionName
		}
		if plannedComponents[index].Order.IsNull() || plannedComponents[index].Order.IsUnknown() {
			plannedComponents[index].Order = currentComponents[index].Order
		}
	}
	return types.ListValueFrom(ctx, types.ObjectType{AttrTypes: recipeComponentTypes}, plannedComponents)
}

func settledRecipeVersionStatus(status string) (bool, error) {
	if pendingRecipeVersionStatus(status) {
		return false, nil
	}
	switch status {
	case "VALIDATED", "FAILED", "RELEASED", "RETIRED":
		return true, nil
	default:
		return false, errors.New("unrecognized recipe version status")
	}
}

func (r *recipeVersionResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var model, plan recipeVersionModel
	response.Diagnostics.Append(request.State.Get(ctx, &model)...)
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	prior := model
	var retry []byte
	if request.Private != nil {
		value, diagnostics := request.Private.GetKey(ctx, recipeVersionUpdateRetryKey)
		retry = value
		response.Diagnostics.Append(diagnostics...)
		if response.Diagnostics.HasError() {
			return
		}
	}
	model.ReleaseType, model.Timeouts = plan.ReleaseType, plan.Timeouts
	stateCtx := context.WithoutCancel(ctx)
	timeout := recipeOperationTimeout(ctx, plan.Timeouts, "update")
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	mutationStarted, complete := false, false
	defer func() {
		if (mutationStarted || string(retry) == "true") && !complete {
			preserveRecipeVersionMutableConfiguration(&model, prior)
		}
		response.Diagnostics.Append(response.State.Set(stateCtx, &model)...)
	}()
	remote, err := r.getVersion(ctx, &model)
	if err != nil {
		addRecipeVersionError(&response.Diagnostics, "update", err, model.ID.ValueString())
		return
	}
	if err := setRecipeVersionState(stateCtx, &model, remote); err != nil {
		addRecipeVersionError(&response.Diagnostics, "update", err, model.ID.ValueString())
		return
	}
	if pendingRecipeVersionStatus(model.Status.ValueString()) {
		if err := r.waitVersion(ctx, &model, timeout, 0, settledRecipeVersionStatus, false); err != nil {
			addRecipeVersionError(&response.Diagnostics, "update", err, model.ID.ValueString())
			return
		}
	}
	status := model.Status.ValueString()
	if (status == "VALIDATED" || status == "RELEASED") && equivalentRecipeVersionConfiguration(ctx, model, plan) {
		components, diagnostics := mergeRecipeComponentPlan(ctx, model.ConfiguredComponents, plan.ConfiguredComponents)
		response.Diagnostics.Append(diagnostics...)
		if response.Diagnostics.HasError() {
			return
		}
		model.Description, model.VolumeSize, model.Integrations, model.ConfiguredComponents = plan.Description, plan.VolumeSize, plan.Integrations, components
		complete = true
		if response.Private != nil {
			response.Diagnostics.Append(response.Private.SetKey(stateCtx, recipeVersionUpdateRetryKey, nil)...)
		}
		return
	}
	if status != "VALIDATED" && status != "FAILED" {
		addRecipeVersionError(&response.Diagnostics, "update", errors.New("version cannot be updated in its current status"), model.ID.ValueString())
		return
	}
	if plan.ConfiguredComponents.IsNull() || plan.ConfiguredComponents.IsUnknown() {
		response.Diagnostics.AddAttributeError(path.Root("configured_components"), "Missing recipe version components", "VEW does not return this version's configured selection. Set configured_components before changing mutable fields so Terraform can send the complete update payload.")
		return
	}
	configured, configDiagnostics := recipeComponentConfig(ctx, request.Config)
	response.Diagnostics.Append(configDiagnostics...)
	if response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.Append(r.resolveRecipeComponentDetails(ctx, &plan, configured)...)
	if response.Diagnostics.HasError() {
		return
	}
	input, diagnostics := recipeVersionInput(ctx, plan)
	response.Diagnostics.Append(diagnostics...)
	if response.Diagnostics.HasError() {
		return
	}
	mutationStarted = true
	if response.Private != nil {
		response.Diagnostics.Append(response.Private.SetKey(stateCtx, recipeVersionUpdateRetryKey, []byte("true"))...)
	}
	action, err := r.client.UpdateRecipeVersion(ctx, model.ProjectID.ValueString(), model.RecipeID.ValueString(), model.ID.ValueString(), input)
	if err != nil {
		addRecipeVersionError(&response.Diagnostics, "update", err, model.ID.ValueString())
		return
	}
	model.Description, model.VolumeSize, model.Integrations, model.ConfiguredComponents = plan.Description, plan.VolumeSize, plan.Integrations, plan.ConfiguredComponents
	if err := r.waitVersion(ctx, &model, timeout, action.RetryAfter, validatedRecipeVersionStatus, false); err != nil {
		addRecipeVersionError(&response.Diagnostics, "update", err, model.ID.ValueString())
		return
	}
	complete = true
	if response.Private != nil {
		response.Diagnostics.Append(response.Private.SetKey(stateCtx, recipeVersionUpdateRetryKey, nil)...)
	}
}
