package recipes

import (
	"context"
	"errors"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func (r *recipeVersionResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	projectID, recipeID, versionID, err := parseRecipeVersionImportID(request.ID)
	if err != nil {
		response.Diagnostics.AddError("Invalid recipe version import ID", "Expected project_id/recipe_id/version_id.")
		return
	}
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("project_id"), projectID)...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("recipe_id"), recipeID)...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), versionID)...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("release_type"), types.StringNull())...)
}

func parseRecipeVersionImportID(value string) (string, string, string, error) {
	parts := strings.Split(value, "/")
	if len(parts) != 3 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" || strings.TrimSpace(parts[2]) == "" {
		return "", "", "", errors.New("expected project_id/recipe_id/version_id")
	}
	return parts[0], parts[1], parts[2], nil
}

func (r *recipeVersionResource) ModifyPlan(ctx context.Context, request resource.ModifyPlanRequest, response *resource.ModifyPlanResponse) {
	if request.State.Raw.IsNull() || request.Plan.Raw.IsNull() {
		return
	}
	var state, plan recipeVersionModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	for _, field := range []struct {
		name     string
		old, new types.String
	}{
		{"project_id", state.ProjectID, plan.ProjectID},
		{"recipe_id", state.RecipeID, plan.RecipeID},
	} {
		if knownStringChange(field.old, field.new) {
			response.RequiresReplace = append(response.RequiresReplace, path.Root(field.name))
		}
	}
	// VEW does not return release_type, so imported versions leave it unset.
	// A recorded create instruction can be forgotten without changing VEW.
	if state.ReleaseType.IsNull() && !plan.ReleaseType.IsNull() && !plan.ReleaseType.IsUnknown() {
		response.Diagnostics.AddAttributeError(path.Root("release_type"), "Cannot set release type on an imported version", "VEW does not return the original release type. Omit release_type for imported recipe versions; the computed name is the observable version identifier.")
		return
	}
	if !state.ReleaseType.IsNull() && !plan.ReleaseType.IsNull() && knownStringChange(state.ReleaseType, plan.ReleaseType) {
		response.RequiresReplace = append(response.RequiresReplace, path.Root("release_type"))
	}
	if state.Status.IsNull() || state.Status.IsUnknown() || state.Status.ValueString() != "RELEASED" {
		// VEW gives a release candidate a new name on every update (for example
		// 1.0.0-rc.1 becomes 1.0.0-rc.2), so the name is only known after apply.
		if !equivalentRecipeVersionConfiguration(ctx, state, plan) {
			response.Diagnostics.Append(response.Plan.SetAttribute(ctx, path.Root("name"), types.StringUnknown())...)
		}
		return
	}
	for _, field := range []struct {
		name    string
		changed bool
	}{
		{"description", knownStringChange(state.Description, plan.Description)},
		{"volume_size", !state.VolumeSize.IsUnknown() && !plan.VolumeSize.IsUnknown() && !state.VolumeSize.Equal(plan.VolumeSize)},
		{"configured_components", !sameRecipeComponentSelection(ctx, state.ConfiguredComponents, plan.ConfiguredComponents)},
		{"integrations", !state.Integrations.IsUnknown() && !plan.Integrations.IsUnknown() && !state.Integrations.Equal(plan.Integrations)},
	} {
		if field.changed {
			response.RequiresReplace = append(response.RequiresReplace, path.Root(field.name))
		}
	}
}

func knownStringChange(old, new types.String) bool {
	return !old.IsUnknown() && !new.IsUnknown() && !old.Equal(new)
}

func equivalentRecipeVersionConfiguration(ctx context.Context, left, right recipeVersionModel) bool {
	return left.Description.Equal(right.Description) && left.VolumeSize.Equal(right.VolumeSize) &&
		left.Integrations.Equal(right.Integrations) && sameRecipeComponentSelection(ctx, left.ConfiguredComponents, right.ConfiguredComponents)
}

func sameRecipeComponentSelection(ctx context.Context, left, right types.List) bool {
	if left.IsNull() || right.IsNull() || left.IsUnknown() || right.IsUnknown() {
		return left.IsNull() && right.IsNull()
	}
	var before, after []recipeComponentModel
	if left.ElementsAs(ctx, &before, false).HasError() || right.ElementsAs(ctx, &after, false).HasError() || len(before) != len(after) {
		return false
	}
	for index := range before {
		if !before[index].ComponentID.Equal(after[index].ComponentID) ||
			!before[index].VersionID.Equal(after[index].VersionID) ||
			!before[index].Type.Equal(after[index].Type) {
			return false
		}
	}
	return true
}
