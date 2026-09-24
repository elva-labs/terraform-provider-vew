package releaseactions

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	"github.com/hashicorp/terraform-plugin-framework/diag"
)

var unreleasedComponent = regexp.MustCompile(`^Version ([A-Za-z0-9][A-Za-z0-9_.:-]{0,127}) of component ([A-Za-z0-9][A-Za-z0-9_.:-]{0,127}) has not been released\.$`)

func addReleaseError(diagnostics *diag.Diagnostics, kind, projectID, parentID, versionID string, err error) {
	operation := "release VEW " + kind + " version"
	parts := []string{"Unable to " + operation + "."}
	for _, target := range []struct{ name, value string }{
		{"project", projectID}, {kind, parentID}, {"version", versionID},
	} {
		if safeIdentifier.MatchString(target.value) {
			parts = append(parts, target.name+" ID: "+target.value+".")
		}
	}
	var api *vew.APIError
	if errors.As(err, &api) {
		if api.Status >= 100 && api.Status <= 599 {
			parts = append(parts, fmt.Sprintf("HTTP status %d.", api.Status))
		}
		if safeIdentifier.MatchString(api.Problem.Code) {
			parts = append(parts, "Problem code: "+api.Problem.Code+".")
		}
		if safeIdentifier.MatchString(api.Problem.RequestID) {
			parts = append(parts, "Request ID: "+api.Problem.RequestID+".")
		}
		if api.Status == 401 || api.Status == 403 {
			parts = append(parts, "Verify the client has the clients/packaging/"+kind+".release scope and is assigned to this project, then retry.")
		}
		if kind == "recipe" && api.Status == 422 && api.Problem.Code == "DOMAIN_VALIDATION_FAILED" {
			if match := unreleasedComponent.FindStringSubmatch(api.Problem.Detail); len(match) == 3 {
				parts = append(parts, "Component "+match[2]+" version "+match[1]+" is not released. Release that component version before retrying the recipe release.")
			}
		}
	}
	// APIError.Error and arbitrary transport errors can include response bodies or
	// credentials, so neither is ever copied into the diagnostic.
	diagnostics.AddError("VEW "+kind+" version release failed", strings.Join(parts, " "))
}
