package recipes

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	vewrecipes "github.com/elva-labs/terraform-provider-vew/internal/vew/recipes"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func (r *recipeVersionResource) getVersion(ctx context.Context, model *recipeVersionModel) (vewrecipes.RecipeVersion, error) {
	return r.client.GetRecipeVersion(ctx, model.ProjectID.ValueString(), model.RecipeID.ValueString(), model.ID.ValueString())
}

func (r *recipeVersionResource) waitVersion(ctx context.Context, model *recipeVersionModel, timeout, delay time.Duration, evaluate vew.StatusEvaluator, missingIsRetired bool) error {
	return r.waiter.Until(ctx, timeout, delay, func(ctx context.Context) (vew.PollResult, error) {
		remote, err := r.getVersion(ctx, model)
		if missingIsRetired && vew.IsNotFound(err) {
			model.Status = types.StringValue("RETIRED")
			return vew.PollResult{Status: "RETIRED"}, nil
		}
		if err != nil {
			return vew.PollResult{}, err
		}
		if err := setRecipeVersionState(context.WithoutCancel(ctx), model, remote); err != nil {
			return vew.PollResult{}, err
		}
		return vew.PollResult{Status: remote.Status, RetryAfter: remote.RetryAfter}, nil
	}, evaluate)
}

func pendingRecipeVersionStatus(status string) bool {
	return status == "CREATING" || status == "CREATED" || status == "TESTING" || status == "UPDATING"
}

func validatedRecipeVersionStatus(status string) (bool, error) {
	if pendingRecipeVersionStatus(status) {
		return false, nil
	}
	switch status {
	case "VALIDATED":
		return true, nil
	case "FAILED", "RELEASED", "RETIRED":
		return false, &vew.TerminalStatusError{Status: status}
	default:
		return false, errors.New("unrecognized recipe version status")
	}
}

var safeRecipeCorrelation = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)

func addRecipeVersionError(diagnostics *diag.Diagnostics, operation string, err error, resourceID string) {
	message := "VEW recipe version " + operation + " failed. Refresh state and retry the operation."
	var terminal *vew.TerminalStatusError
	var timeout *vew.TimeoutError
	var api *vew.APIError
	switch {
	case errors.As(err, &terminal):
		message = "VEW recipe version reached " + terminal.Status + ". Refresh state and retry the operation."
	case errors.As(err, &timeout), errors.Is(err, context.DeadlineExceeded):
		message = "Timed out waiting for VEW recipe version " + operation + ". The latest recoverable state has been retained."
	case errors.Is(err, context.Canceled):
		message = "VEW recipe version " + operation + " was cancelled. The latest recoverable state has been retained."
	case errors.As(err, &api):
		message = fmt.Sprintf("VEW recipe version %s failed (HTTP status %d).", operation, api.Status)
		if safeRecipeCorrelation.MatchString(api.Problem.Code) {
			message += " VEW code: " + api.Problem.Code + "."
		}
		if safeRecipeCorrelation.MatchString(api.Problem.RequestID) {
			message += " Request ID: " + api.Problem.RequestID + "."
		}
	}
	if safeRecipeCorrelation.MatchString(resourceID) {
		message += " Resource ID: " + resourceID + "."
	}
	diagnostics.AddError("Unable to "+operation+" VEW recipe version", message)
}
