package recipes

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func recipeComponentConfig(ctx context.Context, config tfsdk.Config) (types.List, diag.Diagnostics) {
	if config.Schema == nil {
		return types.ListNull(types.ObjectType{AttrTypes: recipeComponentTypes}), nil
	}
	var components types.List
	diagnostics := config.GetAttribute(ctx, path.Root("configured_components"), &components)
	return components, diagnostics
}

// resolveRecipeComponentDetails fills the fields VEW requires in write requests
// but which users can omit from the ordered Terraform selection.
func (r *recipeVersionResource) resolveRecipeComponentDetails(ctx context.Context, model *recipeVersionModel, configured types.List) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	var selected []recipeComponentModel
	diagnostics.Append(model.ConfiguredComponents.ElementsAs(ctx, &selected, false)...)
	if diagnostics.HasError() {
		return diagnostics
	}
	if !configured.IsNull() && !configured.IsUnknown() {
		var source []recipeComponentModel
		diagnostics.Append(configured.ElementsAs(ctx, &source, false)...)
		if diagnostics.HasError() {
			return diagnostics
		}
		if len(source) != len(selected) {
			diagnostics.AddError("Invalid recipe component plan", "The configured component count changed between configuration and apply.")
			return diagnostics
		}
		for index := range source {
			if source[index].ComponentName.IsNull() {
				selected[index].ComponentName = types.StringNull()
			}
			if source[index].VersionName.IsNull() {
				selected[index].VersionName = types.StringNull()
			}
			if source[index].Order.IsNull() {
				selected[index].Order = types.Int64Null()
			}
		}
	}
	for index := range selected {
		component := &selected[index]
		if component.ComponentID.IsNull() || component.ComponentID.IsUnknown() || component.VersionID.IsNull() || component.VersionID.IsUnknown() {
			diagnostics.AddAttributeError(path.Root("configured_components").AtListIndex(index), "Unknown recipe component ID", "Both component_id and version_id must be known before writing a recipe version.")
			return diagnostics
		}
		if component.ComponentName.IsNull() || component.ComponentName.IsUnknown() {
			if r.components == nil {
				diagnostics.AddError("Missing Component API", "The provider cannot resolve recipe component names.")
				return diagnostics
			}
			remote, err := r.components.GetComponent(ctx, model.ProjectID.ValueString(), component.ComponentID.ValueString())
			if err != nil || remote.ID != component.ComponentID.ValueString() || remote.Name == "" {
				diagnostics.AddAttributeError(path.Root("configured_components").AtListIndex(index).AtName("component_id"), "Unable to resolve recipe component", "VEW could not return the component name for this ID in the selected project.")
				return diagnostics
			}
			component.ComponentName = types.StringValue(remote.Name)
		}
		if component.VersionName.IsNull() || component.VersionName.IsUnknown() {
			if r.componentVersions == nil {
				diagnostics.AddError("Missing Component Version API", "The provider cannot resolve recipe component version names.")
				return diagnostics
			}
			remote, err := r.componentVersions.GetComponentVersion(ctx, model.ProjectID.ValueString(), component.ComponentID.ValueString(), component.VersionID.ValueString())
			if err != nil || remote.ID != component.VersionID.ValueString() || remote.ComponentID != component.ComponentID.ValueString() || remote.Name == "" {
				diagnostics.AddAttributeError(path.Root("configured_components").AtListIndex(index).AtName("version_id"), "Unable to resolve recipe component version", "VEW could not return the version name for this component and version ID in the selected project.")
				return diagnostics
			}
			component.VersionName = types.StringValue(remote.Name)
		}
		if component.Order.IsNull() || component.Order.IsUnknown() {
			component.Order = types.Int64Value(int64(index + 1))
		}
	}
	model.ConfiguredComponents, diagnostics = types.ListValueFrom(ctx, types.ObjectType{AttrTypes: recipeComponentTypes}, append([]recipeComponentModel{}, selected...))
	return diagnostics
}
