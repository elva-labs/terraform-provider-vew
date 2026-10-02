package recipes

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	vewrecipes "github.com/elva-labs/terraform-provider-vew/internal/vew/recipes"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

var recipeComponentTypes = map[string]attr.Type{
	"component_id": types.StringType, "component_name": types.StringType,
	"version_id": types.StringType, "version_name": types.StringType,
	"type": types.StringType, "order": types.Int64Type,
}

var recipeTimeoutTypes = map[string]attr.Type{
	"create": types.StringType, "update": types.StringType, "delete": types.StringType,
}

type recipeComponentModel struct {
	ComponentID   types.String `tfsdk:"component_id"`
	ComponentName types.String `tfsdk:"component_name"`
	VersionID     types.String `tfsdk:"version_id"`
	VersionName   types.String `tfsdk:"version_name"`
	Type          types.String `tfsdk:"type"`
	Order         types.Int64  `tfsdk:"order"`
}

type recipeVersionModel struct {
	ID                   types.String `tfsdk:"id"`
	ProjectID            types.String `tfsdk:"project_id"`
	RecipeID             types.String `tfsdk:"recipe_id"`
	Description          types.String `tfsdk:"description"`
	ReleaseType          types.String `tfsdk:"release_type"`
	VolumeSize           types.Int64  `tfsdk:"volume_size"`
	Integrations         types.Set    `tfsdk:"integrations"`
	BaseImageChannel     types.String `tfsdk:"base_image_channel"`
	ConfiguredComponents types.List   `tfsdk:"configured_components"`
	EffectiveComponents  types.List   `tfsdk:"effective_components"`
	Name                 types.String `tfsdk:"name"`
	Status               types.String `tfsdk:"status"`
	CreatedAt            types.String `tfsdk:"created_at"`
	CreatedBy            types.String `tfsdk:"created_by"`
	UpdatedAt            types.String `tfsdk:"updated_at"`
	UpdatedBy            types.String `tfsdk:"updated_by"`
	Timeouts             types.Object `tfsdk:"timeouts"`
}

type recipeTimeoutModel struct {
	Create types.String `tfsdk:"create"`
	Update types.String `tfsdk:"update"`
	Delete types.String `tfsdk:"delete"`
}

func recipeVersionSchema() schema.Schema {
	return schema.Schema{Attributes: map[string]schema.Attribute{
		"id":           schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		"project_id":   recipeReplacementString(),
		"recipe_id":    recipeReplacementString(),
		"description":  schema.StringAttribute{Required: true},
		"release_type": schema.StringAttribute{Optional: true},
		"volume_size":  schema.Int64Attribute{Required: true},
		"integrations": schema.SetAttribute{Optional: true, Computed: true, ElementType: types.StringType,
			Default: setdefault.StaticValue(types.SetValueMust(types.StringType, nil))},
		// Unset, VEW builds a new version on prod and keeps an existing version's channel; recipes
		// outside the base image entries have none.
		"base_image_channel": schema.StringAttribute{Optional: true, Computed: true,
			Description:   "Release channel of the base image the parent image comes from, prod or test. Only for recipes on a base image entry; VEW uses prod when unset.",
			PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		"configured_components": recipeComponentsAttribute(true),
		"effective_components":  recipeComponentsAttribute(false),
		"name":                  schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		"status":                schema.StringAttribute{Computed: true},
		"created_at":            schema.StringAttribute{Computed: true},
		"created_by":            schema.StringAttribute{Computed: true},
		"updated_at":            schema.StringAttribute{Computed: true},
		"updated_by":            schema.StringAttribute{Computed: true},
	}, Blocks: map[string]schema.Block{
		"timeouts": schema.SingleNestedBlock{Attributes: map[string]schema.Attribute{
			"create": schema.StringAttribute{Optional: true},
			"update": schema.StringAttribute{Optional: true},
			"delete": schema.StringAttribute{Optional: true},
		}},
	}}
}

func recipeComponentsAttribute(configured bool) schema.ListNestedAttribute {
	if configured {
		return schema.ListNestedAttribute{Required: true, NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
			"component_id":   schema.StringAttribute{Required: true},
			"version_id":     schema.StringAttribute{Required: true},
			"type":           schema.StringAttribute{Required: true},
			"component_name": schema.StringAttribute{Optional: true, Computed: true},
			"version_name":   schema.StringAttribute{Optional: true, Computed: true},
			"order":          schema.Int64Attribute{Optional: true, Computed: true},
		}}}
	}
	return schema.ListNestedAttribute{Computed: true, NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
		"component_id":   schema.StringAttribute{Computed: true},
		"component_name": schema.StringAttribute{Computed: true},
		"version_id":     schema.StringAttribute{Computed: true},
		"version_name":   schema.StringAttribute{Computed: true},
		"type":           schema.StringAttribute{Computed: true},
		"order":          schema.Int64Attribute{Computed: true},
	}}}
}

func (r *recipeVersionResource) ValidateConfig(ctx context.Context, request resource.ValidateConfigRequest, response *resource.ValidateConfigResponse) {
	var config recipeVersionModel
	response.Diagnostics.Append(request.Config.Get(ctx, &config)...)
	if response.Diagnostics.HasError() {
		return
	}
	for name, value := range map[string]types.String{
		"project_id": config.ProjectID, "recipe_id": config.RecipeID,
		"description": config.Description, "release_type": config.ReleaseType,
	} {
		if !value.IsUnknown() && !value.IsNull() && strings.TrimSpace(value.ValueString()) == "" {
			response.Diagnostics.AddAttributeError(path.Root(name), "Empty recipe version value", name+" must be non-empty.")
		}
	}
	if !config.ReleaseType.IsUnknown() && !config.ReleaseType.IsNull() {
		switch config.ReleaseType.ValueString() {
		case "MAJOR", "MINOR", "PATCH":
		default:
			response.Diagnostics.AddAttributeError(path.Root("release_type"), "Invalid recipe version release type", "release_type must be MAJOR, MINOR, or PATCH.")
		}
	}
	if !config.BaseImageChannel.IsUnknown() && !config.BaseImageChannel.IsNull() {
		switch config.BaseImageChannel.ValueString() {
		case "prod", "test":
		default:
			response.Diagnostics.AddAttributeError(path.Root("base_image_channel"), "Invalid base image channel", "base_image_channel must be prod or test.")
		}
	}
	if !config.VolumeSize.IsUnknown() && !config.VolumeSize.IsNull() && (config.VolumeSize.ValueInt64() < 8 || config.VolumeSize.ValueInt64() > 500) {
		response.Diagnostics.AddAttributeError(path.Root("volume_size"), "Invalid recipe version volume size", "volume_size must be between 8 and 500 GB.")
	}
	if !config.ConfiguredComponents.IsUnknown() && !config.ConfiguredComponents.IsNull() {
		response.Diagnostics.Append(validateRecipeComponents(ctx, config.ConfiguredComponents)...)
	}
	if !config.Integrations.IsUnknown() && !config.Integrations.IsNull() {
		var integrations []string
		response.Diagnostics.Append(config.Integrations.ElementsAs(ctx, &integrations, false)...)
		for _, integration := range integrations {
			if strings.TrimSpace(integration) == "" {
				response.Diagnostics.AddAttributeError(path.Root("integrations"), "Empty recipe integration", "integrations must contain non-empty values.")
			}
		}
	}
	response.Diagnostics.Append(validateRecipeTimeouts(ctx, config.Timeouts)...)
}

func validateRecipeComponents(ctx context.Context, value types.List) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	var components []recipeComponentModel
	diagnostics.Append(value.ElementsAs(ctx, &components, false)...)
	if diagnostics.HasError() {
		return diagnostics
	}
	orders := make(map[int64]bool)
	for index, component := range components {
		for name, field := range map[string]types.String{
			"component_id": component.ComponentID, "component_name": component.ComponentName,
			"version_id": component.VersionID, "version_name": component.VersionName, "type": component.Type,
		} {
			if !field.IsUnknown() && !field.IsNull() && strings.TrimSpace(field.ValueString()) == "" {
				diagnostics.AddAttributeError(path.Root("configured_components").AtListIndex(index).AtName(name), "Empty recipe component value", name+" must be non-empty.")
			}
		}
		if component.Order.IsUnknown() || component.Order.IsNull() {
			continue
		}
		order := component.Order.ValueInt64()
		if order <= 0 {
			diagnostics.AddAttributeError(path.Root("configured_components").AtListIndex(index).AtName("order"), "Invalid recipe component order", "order must be positive.")
		} else if orders[order] {
			diagnostics.AddAttributeError(path.Root("configured_components").AtListIndex(index).AtName("order"), "Duplicate recipe component order", "order must be unique.")
		}
		orders[order] = true
	}
	return diagnostics
}

func expandRecipeComponents(ctx context.Context, value types.List) ([]vewrecipes.ComponentVersion, diag.Diagnostics) {
	var models []recipeComponentModel
	diagnostics := value.ElementsAs(ctx, &models, false)
	if diagnostics.HasError() {
		return nil, diagnostics
	}
	components := make([]vewrecipes.ComponentVersion, 0, len(models))
	for _, model := range models {
		components = append(components, vewrecipes.ComponentVersion{
			ComponentID: model.ComponentID.ValueString(), ComponentName: model.ComponentName.ValueString(),
			VersionID: model.VersionID.ValueString(), VersionName: model.VersionName.ValueString(),
			Type: model.Type.ValueString(), Order: model.Order.ValueInt64(),
		})
	}
	return components, diagnostics
}

func recipeComponentList(ctx context.Context, components []vewrecipes.ComponentVersion) (types.List, diag.Diagnostics) {
	models := make([]recipeComponentModel, 0, len(components))
	for _, component := range components {
		models = append(models, recipeComponentModel{
			ComponentID: types.StringValue(component.ComponentID), ComponentName: types.StringValue(component.ComponentName),
			VersionID: types.StringValue(component.VersionID), VersionName: types.StringValue(component.VersionName),
			Type: types.StringValue(component.Type), Order: types.Int64Value(component.Order),
		})
	}
	return types.ListValueFrom(ctx, types.ObjectType{AttrTypes: recipeComponentTypes}, models)
}

func recipeVersionInput(ctx context.Context, model recipeVersionModel) (vewrecipes.UpdateRecipeVersionInput, diag.Diagnostics) {
	components, diagnostics := expandRecipeComponents(ctx, model.ConfiguredComponents)
	var integrations []string
	diagnostics.Append(model.Integrations.ElementsAs(ctx, &integrations, false)...)
	if diagnostics.HasError() {
		return vewrecipes.UpdateRecipeVersionInput{}, diagnostics
	}
	sort.Strings(integrations)
	channel := ""
	if !model.BaseImageChannel.IsNull() && !model.BaseImageChannel.IsUnknown() {
		channel = model.BaseImageChannel.ValueString()
	}
	return vewrecipes.UpdateRecipeVersionInput{Components: components, Description: model.Description.ValueString(), VolumeSize: strconv.FormatInt(model.VolumeSize.ValueInt64(), 10), Integrations: integrations, BaseImageChannel: channel}, diagnostics
}

func validateRecipeTimeouts(ctx context.Context, timeouts types.Object) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	if timeouts.IsNull() || timeouts.IsUnknown() {
		return diagnostics
	}
	var values recipeTimeoutModel
	diagnostics.Append(timeouts.As(ctx, &values, basetypes.ObjectAsOptions{})...)
	if diagnostics.HasError() {
		return diagnostics
	}
	for name, value := range map[string]types.String{"create": values.Create, "update": values.Update, "delete": values.Delete} {
		if value.IsNull() || value.IsUnknown() {
			continue
		}
		duration, err := time.ParseDuration(value.ValueString())
		if err != nil || duration <= 0 {
			diagnostics.AddAttributeError(path.Root("timeouts").AtName(name), "Invalid recipe version timeout", fmt.Sprintf("timeouts.%s must be a positive Go duration.", name))
		}
	}
	return diagnostics
}

func recipeOperationTimeout(ctx context.Context, timeouts types.Object, operation string) time.Duration {
	defaults := map[string]time.Duration{"create": time.Hour, "update": time.Hour, "delete": 30 * time.Minute}
	defaultTimeout := defaults[operation]
	if timeouts.IsNull() || timeouts.IsUnknown() {
		return defaultTimeout
	}
	var values recipeTimeoutModel
	if diagnostics := timeouts.As(ctx, &values, basetypes.ObjectAsOptions{}); diagnostics.HasError() {
		return defaultTimeout
	}
	value := map[string]types.String{"create": values.Create, "update": values.Update, "delete": values.Delete}[operation]
	if value.IsNull() || value.IsUnknown() {
		return defaultTimeout
	}
	duration, err := time.ParseDuration(value.ValueString())
	if err != nil || duration <= 0 {
		return defaultTimeout
	}
	return duration
}
