package recipes

import (
	"errors"
	"fmt"
	"regexp"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
)

var blockedRecipeArchive = regexp.MustCompile(`^Recipe ([A-Za-z0-9][A-Za-z0-9_.:-]{0,127}) cannot be retired because recipe version ([A-Za-z0-9][A-Za-z0-9_.:-]{0,127}) is in (CREATING|CREATED|FAILED|RELEASED|TESTING|UPDATING|VALIDATED) status\.$`)

func recipeArchiveDiagnostic(recipeID string, err error) (string, string) {
	fallback := recipeAPIDiagnostic("archive", err)
	var apiError *vew.APIError
	if !errors.As(err, &apiError) {
		return "Unable to archive VEW recipe", fallback
	}
	if apiError.Status == 422 && apiError.Problem.Code == "DOMAIN_VALIDATION_FAILED" {
		match := blockedRecipeArchive.FindStringSubmatch(apiError.Problem.Detail)
		if len(match) == 4 && match[1] == recipeID {
			return "Recipe has a non-retired version", fmt.Sprintf("VEW cannot archive this recipe because version %s is %s. Retire every version of the recipe, then retry deletion. If Terraform manages the versions, reference this recipe's ID in their recipe_id attributes so Terraform retires them first. %s", match[2], match[3], fallback)
		}
	}
	if apiError.Problem.Code == "ACTIVE_VERSION" {
		return "Recipe has a non-retired version", "Retire every version of this recipe before retrying deletion. If Terraform manages the versions, reference this recipe's ID in their recipe_id attributes so Terraform retires them first. " + fallback
	}
	return "Unable to archive VEW recipe", fallback
}
