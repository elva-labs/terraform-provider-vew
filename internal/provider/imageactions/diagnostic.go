package imageactions

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	"github.com/hashicorp/terraform-plugin-framework/action"
)

func addImageBuildError(response *action.InvokeResponse, phase, projectID, pipelineID, imageID, key string, err error) {
	parts := []string{"Unable to complete VEW image build."}
	parts = append(parts, strings.TrimSpace(safeTarget("project", projectID, key)))
	parts = append(parts, strings.TrimSpace(safeTarget("pipeline", pipelineID, key)))
	if imageID != "" {
		parts = append(parts, strings.TrimSpace(safeTarget("image", imageID, key)))
	}
	var api *vew.APIError
	if errors.As(err, &api) {
		if api.Status >= 100 && api.Status <= 599 {
			parts = append(parts, fmt.Sprintf("HTTP status %d.", api.Status))
		}
		if safeIdentifier.MatchString(api.Problem.Code) && !containsKey(api.Problem.Code, key) {
			parts = append(parts, "Problem code: "+api.Problem.Code+".")
		}
		if safeIdentifier.MatchString(api.Problem.RequestID) && !containsKey(api.Problem.RequestID, key) {
			parts = append(parts, "Request ID: "+api.Problem.RequestID+".")
		}
		if api.Status == 401 || api.Status == 403 {
			scope := "clients/packaging/pipeline.execute"
			if phase == "read" {
				scope = "clients/packaging/pipeline.read"
			}
			parts = append(parts, "Verify the client has the "+scope+" scope and is assigned to this project.")
		}
	}
	switch {
	case errors.Is(err, errMissingUpstreamID):
		parts = append(parts, "The image reached CREATED without an upstream ID.")
	case errors.Is(err, errUnsupportedStatus):
		parts = append(parts, "The image returned an unsupported status.")
	case errors.Is(err, errImageIDMismatch):
		parts = append(parts, "The image read returned a different image ID.")
	default:
		var terminal *vew.TerminalStatusError
		var timeout *vew.TimeoutError
		switch {
		case errors.As(err, &terminal):
			if safeStatus(terminal.Status) {
				parts = append(parts, "The image reached terminal status "+terminal.Status+".")
			} else {
				parts = append(parts, "The image reached a terminal status.")
			}
		case errors.As(err, &timeout):
			parts = append(parts, "Timed out waiting for image creation.")
			if safeStatus(timeout.LastStatus) {
				parts = append(parts, "Last status: "+timeout.LastStatus+".")
			}
		case errors.Is(err, context.Canceled):
			parts = append(parts, "The invocation was canceled.")
		case errors.Is(err, context.DeadlineExceeded):
			parts = append(parts, "The request deadline expired.")
		}
	}
	parts = append(parts, "Retry with the same idempotency_key to resume this build safely.")
	response.Diagnostics.AddError("VEW image build failed", strings.Join(nonempty(parts), " "))
}

func nonempty(parts []string) []string {
	result := parts[:0]
	for _, part := range parts {
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}
