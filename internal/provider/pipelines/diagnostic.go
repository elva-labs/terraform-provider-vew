package pipelines

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	"github.com/hashicorp/terraform-plugin-framework/diag"
)

var (
	safeCorrelation  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)
	unreleasedDetail = regexp.MustCompile(`^Version ([A-Za-z0-9][A-Za-z0-9_.:-]{0,127}) of recipe ([A-Za-z0-9][A-Za-z0-9_.:-]{0,127}) has not been released\.$`)
)

func unreleasedVersion(err error, recipeID, versionID string) bool {
	var api *vew.APIError
	if !errors.As(err, &api) || api.Status != 422 || api.Problem.Code != "DOMAIN_VALIDATION_FAILED" || !safeCorrelation.MatchString(recipeID) || !safeCorrelation.MatchString(versionID) {
		return false
	}
	match := unreleasedDetail.FindStringSubmatch(api.Problem.Detail)
	return len(match) == 3 && match[1] == versionID && match[2] == recipeID
}

func addPipelineError(diagnostics *diag.Diagnostics, operation string, err error, resourceID, recipeID, versionID string) {
	if (operation == "create" || operation == "update") && unreleasedVersion(err, recipeID, versionID) {
		diagnostics.AddError("Recipe version is not released", fmt.Sprintf("Recipe %s version %s must be released before retrying terraform apply. Invoke the provider's vew_recipe_version_release action for this version, then refresh and retry.", recipeID, versionID))
		return
	}
	detail := "VEW pipeline " + operation + " failed. Refresh state and retry."
	var terminal *vew.TerminalStatusError
	var timeout *vew.TimeoutError
	var api *vew.APIError
	switch {
	case errors.As(err, &terminal):
		if safeCorrelation.MatchString(terminal.Status) {
			detail = "VEW pipeline reached " + terminal.Status + ". The latest recoverable state has been retained."
		}
	case errors.As(err, &timeout), errors.Is(err, context.DeadlineExceeded):
		detail = "Timed out waiting for VEW pipeline " + operation + ". The latest recoverable state has been retained."
	case errors.Is(err, context.Canceled):
		detail = "VEW pipeline " + operation + " was cancelled. The latest recoverable state has been retained."
	case errors.As(err, &api):
		detail = fmt.Sprintf("VEW pipeline %s failed (HTTP status %d).", operation, api.Status)
		if safeCorrelation.MatchString(api.Problem.Code) {
			detail += " VEW code: " + api.Problem.Code + "."
		}
		if safeCorrelation.MatchString(api.Problem.RequestID) {
			detail += " Request ID: " + api.Problem.RequestID + "."
		}
	}
	if safeCorrelation.MatchString(resourceID) {
		detail += " Resource ID: " + resourceID + "."
	}
	diagnostics.AddError("Unable to "+operation+" VEW pipeline", detail)
}
