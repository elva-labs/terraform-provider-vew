package recipes

import (
	"context"
	"errors"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

func retiredRecipeVersionStatus(status string) (bool, error) {
	if pendingRecipeVersionStatus(status) || status == "VALIDATED" || status == "RELEASED" {
		return false, nil
	}
	switch status {
	case "RETIRED":
		return true, nil
	case "FAILED":
		return false, &vew.TerminalStatusError{Status: status}
	default:
		return false, errors.New("unrecognized recipe version status")
	}
}

func (r *recipeVersionResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	var model recipeVersionModel
	response.Diagnostics.Append(request.State.Get(ctx, &model)...)
	if response.Diagnostics.HasError() {
		return
	}
	stateCtx := context.WithoutCancel(ctx)
	timeout := recipeOperationTimeout(ctx, model.Timeouts, "delete")
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
	remote, err := r.getVersion(ctx, &model)
	if vew.IsNotFound(err) || (err == nil && remote.Status == "RETIRED") {
		removed = true
		return
	}
	if err != nil {
		addRecipeVersionError(&response.Diagnostics, "delete", err, model.ID.ValueString())
		return
	}
	if err := setRecipeVersionState(stateCtx, &model, remote); err != nil {
		addRecipeVersionError(&response.Diagnostics, "delete", err, model.ID.ValueString())
		return
	}
	if pendingRecipeVersionStatus(model.Status.ValueString()) {
		if err := r.waitVersion(ctx, &model, timeout, 0, settledRecipeVersionStatus, true); err != nil {
			addRecipeVersionError(&response.Diagnostics, "delete", err, model.ID.ValueString())
			return
		}
	}
	if model.Status.ValueString() == "RETIRED" {
		removed = true
		return
	}
	if status := model.Status.ValueString(); status != "VALIDATED" && status != "FAILED" && status != "RELEASED" {
		addRecipeVersionError(&response.Diagnostics, "delete", errors.New("version cannot be retired in its current status"), model.ID.ValueString())
		return
	}
	action, err := r.client.RetireRecipeVersion(ctx, model.ProjectID.ValueString(), model.RecipeID.ValueString(), model.ID.ValueString())
	if vew.IsNotFound(err) {
		removed = true
		return
	}
	if err != nil {
		addRecipeVersionError(&response.Diagnostics, "delete", err, model.ID.ValueString())
		return
	}
	if err := r.waitVersion(ctx, &model, timeout, action.RetryAfter, retiredRecipeVersionStatus, true); err != nil {
		addRecipeVersionError(&response.Diagnostics, "delete", err, model.ID.ValueString())
		return
	}
	removed = true
}
