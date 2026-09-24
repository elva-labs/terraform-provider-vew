package pipelines

import (
	"context"
	"errors"
	"time"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	vewpipelines "github.com/elva-labs/terraform-provider-vew/internal/vew/pipelines"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const updateRetryKey = "pipeline_update_retry"

func (r *pipelineResource) waitPipeline(ctx context.Context, model *pipelineModel, timeout, delay time.Duration, evaluate vew.StatusEvaluator, missingIsRetired bool) error {
	return r.waiter.Until(ctx, timeout, delay, func(ctx context.Context) (vew.PollResult, error) {
		remote, err := r.client.GetPipeline(ctx, model.ProjectID.ValueString(), model.ID.ValueString())
		if missingIsRetired && vew.IsNotFound(err) {
			model.Status = types.StringValue("RETIRED")
			return vew.PollResult{Status: "RETIRED"}, nil
		}
		if err != nil {
			return vew.PollResult{}, err
		}
		if err := setPipelineState(context.WithoutCancel(ctx), model, remote); err != nil {
			return vew.PollResult{}, err
		}
		return vew.PollResult{Status: remote.Status, RetryAfter: remote.RetryAfter}, nil
	}, evaluate)
}

func pendingStatus(status string) bool { return status == "CREATING" || status == "UPDATING" }

func createdStatus(status string) (bool, error) {
	if pendingStatus(status) {
		return false, nil
	}
	switch status {
	case "CREATED":
		return true, nil
	case "FAILED", "RETIRED":
		return false, &vew.TerminalStatusError{Status: status}
	default:
		return false, errors.New("unrecognized pipeline status")
	}
}

func settledStatus(status string) (bool, error) {
	if pendingStatus(status) {
		return false, nil
	}
	switch status {
	case "CREATED", "FAILED", "RETIRED":
		return true, nil
	default:
		return false, errors.New("unrecognized pipeline status")
	}
}

func retiredStatus(status string) (bool, error) {
	if pendingStatus(status) || status == "CREATED" {
		return false, nil
	}
	switch status {
	case "RETIRED":
		return true, nil
	case "FAILED":
		return false, &vew.TerminalStatusError{Status: status}
	default:
		return false, errors.New("unrecognized pipeline status")
	}
}

func equivalentMutable(left, right pipelineModel) bool {
	return left.RecipeVersionID.Equal(right.RecipeVersionID) && left.BuildInstanceTypes.Equal(right.BuildInstanceTypes) && left.Schedule.Equal(right.Schedule) && left.ProductID.Equal(right.ProductID)
}

func preserveMutable(model *pipelineModel, prior pipelineModel) {
	model.RecipeVersionID, model.BuildInstanceTypes, model.Schedule, model.ProductID = prior.RecipeVersionID, prior.BuildInstanceTypes, prior.Schedule, prior.ProductID
}

func (r *pipelineResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var model, plan pipelineModel
	response.Diagnostics.Append(request.State.Get(ctx, &model)...)
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	prior := model
	var retry []byte
	if request.Private != nil {
		value, diagnostics := request.Private.GetKey(ctx, updateRetryKey)
		retry = value
		response.Diagnostics.Append(diagnostics...)
		if response.Diagnostics.HasError() {
			return
		}
	}
	model.Timeouts = plan.Timeouts
	stateCtx := context.WithoutCancel(ctx)
	timeout := operationTimeout(ctx, plan.Timeouts, "update")
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	mutationStarted, complete := false, false
	defer func() {
		if (mutationStarted || string(retry) == "true") && !complete {
			preserveMutable(&model, prior)
		}
		response.Diagnostics.Append(response.State.Set(stateCtx, &model)...)
	}()
	remote, err := r.client.GetPipeline(ctx, model.ProjectID.ValueString(), model.ID.ValueString())
	if err != nil {
		addPipelineError(&response.Diagnostics, "update", err, model.ID.ValueString(), plan.RecipeID.ValueString(), plan.RecipeVersionID.ValueString())
		return
	}
	if err := setPipelineState(stateCtx, &model, remote); err != nil {
		addPipelineError(&response.Diagnostics, "update", err, model.ID.ValueString(), plan.RecipeID.ValueString(), plan.RecipeVersionID.ValueString())
		return
	}
	if pendingStatus(model.Status.ValueString()) {
		if err := r.waitPipeline(ctx, &model, timeout, 0, settledStatus, false); err != nil {
			addPipelineError(&response.Diagnostics, "update", err, model.ID.ValueString(), plan.RecipeID.ValueString(), plan.RecipeVersionID.ValueString())
			return
		}
	}
	if model.Status.ValueString() == "CREATED" && equivalentMutable(model, plan) {
		preserveMutable(&model, plan)
		complete = true
		if response.Private != nil {
			response.Diagnostics.Append(response.Private.SetKey(stateCtx, updateRetryKey, nil)...)
		}
		return
	}
	if model.Status.ValueString() != "CREATED" && model.Status.ValueString() != "FAILED" {
		addPipelineError(&response.Diagnostics, "update", errors.New("pipeline cannot be updated in current status"), model.ID.ValueString(), plan.RecipeID.ValueString(), plan.RecipeVersionID.ValueString())
		return
	}
	instanceTypes, err := configuredInstanceTypes(ctx, plan.BuildInstanceTypes)
	if err != nil {
		addPipelineError(&response.Diagnostics, "update", err, model.ID.ValueString(), plan.RecipeID.ValueString(), plan.RecipeVersionID.ValueString())
		return
	}
	input := vewpipelines.UpdatePipelineInput{RecipeVersionID: plan.RecipeVersionID.ValueString(), BuildInstanceTypes: instanceTypes, Schedule: plan.Schedule.ValueString(), ProductID: productPointer(plan.ProductID)}
	mutationStarted = true
	if response.Private != nil {
		response.Diagnostics.Append(response.Private.SetKey(stateCtx, updateRetryKey, []byte("true"))...)
	}
	action, err := r.client.UpdatePipeline(ctx, model.ProjectID.ValueString(), model.ID.ValueString(), input)
	if err != nil {
		addPipelineError(&response.Diagnostics, "update", err, model.ID.ValueString(), plan.RecipeID.ValueString(), plan.RecipeVersionID.ValueString())
		return
	}
	model.Status = types.StringValue("UPDATING")
	model.RecipeVersionID, model.BuildInstanceTypes, model.Schedule, model.ProductID = plan.RecipeVersionID, plan.BuildInstanceTypes, plan.Schedule, plan.ProductID
	if err := r.waitPipeline(ctx, &model, timeout, action.RetryAfter, createdStatus, false); err != nil {
		addPipelineError(&response.Diagnostics, "update", err, model.ID.ValueString(), plan.RecipeID.ValueString(), plan.RecipeVersionID.ValueString())
		return
	}
	complete = true
	if response.Private != nil {
		response.Diagnostics.Append(response.Private.SetKey(stateCtx, updateRetryKey, nil)...)
	}
}

func (r *pipelineResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	var model pipelineModel
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
	remote, err := r.client.GetPipeline(ctx, model.ProjectID.ValueString(), model.ID.ValueString())
	if vew.IsNotFound(err) || (err == nil && remote.Status == "RETIRED") {
		removed = true
		return
	}
	if err != nil {
		addPipelineError(&response.Diagnostics, "delete", err, model.ID.ValueString(), model.RecipeID.ValueString(), model.RecipeVersionID.ValueString())
		return
	}
	if err := setPipelineState(stateCtx, &model, remote); err != nil {
		addPipelineError(&response.Diagnostics, "delete", err, model.ID.ValueString(), model.RecipeID.ValueString(), model.RecipeVersionID.ValueString())
		return
	}
	if pendingStatus(model.Status.ValueString()) {
		if err := r.waitPipeline(ctx, &model, timeout, 0, settledStatus, true); err != nil {
			addPipelineError(&response.Diagnostics, "delete", err, model.ID.ValueString(), model.RecipeID.ValueString(), model.RecipeVersionID.ValueString())
			return
		}
	}
	if model.Status.ValueString() == "RETIRED" {
		removed = true
		return
	}
	if model.Status.ValueString() != "CREATED" && model.Status.ValueString() != "FAILED" {
		addPipelineError(&response.Diagnostics, "delete", errors.New("pipeline cannot be retired in current status"), model.ID.ValueString(), model.RecipeID.ValueString(), model.RecipeVersionID.ValueString())
		return
	}
	action, err := r.client.RetirePipeline(ctx, model.ProjectID.ValueString(), model.ID.ValueString())
	if vew.IsNotFound(err) {
		removed = true
		return
	}
	if err != nil {
		addPipelineError(&response.Diagnostics, "delete", err, model.ID.ValueString(), model.RecipeID.ValueString(), model.RecipeVersionID.ValueString())
		return
	}
	if err := r.waitPipeline(ctx, &model, timeout, action.RetryAfter, retiredStatus, true); err != nil {
		addPipelineError(&response.Diagnostics, "delete", err, model.ID.ValueString(), model.RecipeID.ValueString(), model.RecipeVersionID.ValueString())
		return
	}
	removed = true
}
