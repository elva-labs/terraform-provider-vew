package projectaccounts

import (
	"context"
	"errors"
	"fmt"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	"github.com/hashicorp/terraform-plugin-framework/diag"
)

func addAccountError(d *diag.Diagnostics, operation string, err error, accountID string) {
	message := "VEW project account " + operation + " failed. Refresh state and retry."
	var terminal *vew.TerminalStatusError
	var timeout *vew.TimeoutError
	var api *vew.APIError
	switch {
	case errors.As(err, &terminal):
		switch terminal.Status {
		case "Failed", "FAILED", "Active/Failed", "ACTIVE/FAILED", "Inactive", "Archived":
			message = "VEW project account reached " + terminal.Status + ". Its latest recoverable state was retained."
		default:
			message = "VEW project account reached an unsupported terminal status. Its latest recoverable state was retained."
		}
	case errors.As(err, &timeout), errors.Is(err, context.DeadlineExceeded):
		message = "Timed out waiting for VEW project account " + operation + ". Its latest recoverable state was retained."
	case errors.Is(err, context.Canceled):
		message = "VEW project account " + operation + " was cancelled. Its latest recoverable state was retained."
	case errors.As(err, &api):
		message = fmt.Sprintf("VEW project account %s failed (HTTP status %d).", operation, api.Status)
		if safeCorrelation.MatchString(api.Problem.Code) {
			message += " VEW code: " + api.Problem.Code + "."
		}
		if safeCorrelation.MatchString(api.Problem.RequestID) {
			message += " Request ID: " + api.Problem.RequestID + "."
		}
	}
	if safeCorrelation.MatchString(accountID) {
		message += " Account ID: " + accountID + "."
	}
	d.AddError("Unable to "+operation+" VEW project account", message)
}
