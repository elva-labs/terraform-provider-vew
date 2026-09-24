package pipelines

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

type pipelineModel struct {
	ID                      types.String `tfsdk:"id"`
	ProjectID               types.String `tfsdk:"project_id"`
	Name                    types.String `tfsdk:"name"`
	Description             types.String `tfsdk:"description"`
	RecipeID                types.String `tfsdk:"recipe_id"`
	RecipeName              types.String `tfsdk:"recipe_name"`
	RecipeVersionID         types.String `tfsdk:"recipe_version_id"`
	RecipeVersionName       types.String `tfsdk:"recipe_version_name"`
	BuildInstanceTypes      types.List   `tfsdk:"build_instance_types"`
	Schedule                types.String `tfsdk:"schedule"`
	ProductID               types.String `tfsdk:"product_id"`
	Status                  types.String `tfsdk:"status"`
	DistributionConfigARN   types.String `tfsdk:"distribution_config_arn"`
	InfrastructureConfigARN types.String `tfsdk:"infrastructure_config_arn"`
	PipelineARN             types.String `tfsdk:"pipeline_arn"`
	CreatedAt               types.String `tfsdk:"created_at"`
	CreatedBy               types.String `tfsdk:"created_by"`
	UpdatedAt               types.String `tfsdk:"updated_at"`
	UpdatedBy               types.String `tfsdk:"updated_by"`
	Timeouts                types.Object `tfsdk:"timeouts"`
}

type timeoutModel struct {
	Create types.String `tfsdk:"create"`
	Update types.String `tfsdk:"update"`
	Delete types.String `tfsdk:"delete"`
}

var timeoutTypes = map[string]attr.Type{"create": types.StringType, "update": types.StringType, "delete": types.StringType}

func pipelineSchema() schema.Schema {
	replace := func() schema.StringAttribute {
		return schema.StringAttribute{Required: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}}
	}
	return schema.Schema{Attributes: map[string]schema.Attribute{
		"id":         schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		"project_id": replace(), "name": replace(), "description": replace(), "recipe_id": replace(),
		"recipe_version_id":    schema.StringAttribute{Required: true},
		"build_instance_types": schema.ListAttribute{Required: true, ElementType: types.StringType},
		"schedule":             schema.StringAttribute{Required: true},
		"product_id":           schema.StringAttribute{Optional: true},
		"recipe_name":          schema.StringAttribute{Computed: true}, "recipe_version_name": schema.StringAttribute{Computed: true},
		"status":                  schema.StringAttribute{Computed: true},
		"distribution_config_arn": schema.StringAttribute{Computed: true}, "infrastructure_config_arn": schema.StringAttribute{Computed: true}, "pipeline_arn": schema.StringAttribute{Computed: true},
		"created_at": schema.StringAttribute{Computed: true}, "created_by": schema.StringAttribute{Computed: true},
		"updated_at": schema.StringAttribute{Computed: true}, "updated_by": schema.StringAttribute{Computed: true},
	}, Blocks: map[string]schema.Block{"timeouts": schema.SingleNestedBlock{Attributes: map[string]schema.Attribute{
		"create": schema.StringAttribute{Optional: true}, "update": schema.StringAttribute{Optional: true}, "delete": schema.StringAttribute{Optional: true},
	}}}}
}

func (r *pipelineResource) ValidateConfig(ctx context.Context, request resource.ValidateConfigRequest, response *resource.ValidateConfigResponse) {
	var model pipelineModel
	response.Diagnostics.Append(request.Config.Get(ctx, &model)...)
	if response.Diagnostics.HasError() {
		return
	}
	for name, value := range map[string]types.String{"project_id": model.ProjectID, "name": model.Name, "description": model.Description, "recipe_id": model.RecipeID, "recipe_version_id": model.RecipeVersionID, "schedule": model.Schedule, "product_id": model.ProductID} {
		if !value.IsNull() && !value.IsUnknown() && strings.TrimSpace(value.ValueString()) == "" {
			response.Diagnostics.AddAttributeError(path.Root(name), "Empty pipeline value", name+" must be non-empty.")
		}
	}
	if !model.Schedule.IsNull() && !model.Schedule.IsUnknown() && (len(strings.Fields(model.Schedule.ValueString())) != 6 || strings.Contains(model.Schedule.ValueString(), "(") || strings.Contains(model.Schedule.ValueString(), ")")) {
		response.Diagnostics.AddAttributeError(path.Root("schedule"), "Invalid pipeline schedule", "schedule must be a six-field VEW expression without a cron(...) wrapper.")
	}
	if !model.BuildInstanceTypes.IsNull() && !model.BuildInstanceTypes.IsUnknown() {
		var instances []types.String
		response.Diagnostics.Append(model.BuildInstanceTypes.ElementsAs(ctx, &instances, false)...)
		if len(instances) == 0 {
			response.Diagnostics.AddAttributeError(path.Root("build_instance_types"), "Empty pipeline instance types", "Provide at least one build instance type.")
		}
		for i, value := range instances {
			if !value.IsUnknown() && (value.IsNull() || strings.TrimSpace(value.ValueString()) == "") {
				response.Diagnostics.AddAttributeError(path.Root("build_instance_types").AtListIndex(i), "Empty pipeline instance type", "Every build instance type must be non-empty.")
			}
		}
	}
	response.Diagnostics.Append(validateTimeouts(ctx, model.Timeouts)...)
}

func validateTimeouts(ctx context.Context, value types.Object) diag.Diagnostics {
	var result diag.Diagnostics
	if value.IsNull() || value.IsUnknown() {
		return result
	}
	var timeouts timeoutModel
	result.Append(value.As(ctx, &timeouts, basetypes.ObjectAsOptions{})...)
	if result.HasError() {
		return result
	}
	for name, field := range map[string]types.String{"create": timeouts.Create, "update": timeouts.Update, "delete": timeouts.Delete} {
		if field.IsNull() || field.IsUnknown() {
			continue
		}
		duration, err := time.ParseDuration(field.ValueString())
		if err != nil || duration <= 0 {
			result.AddAttributeError(path.Root("timeouts").AtName(name), "Invalid pipeline timeout", fmt.Sprintf("timeouts.%s must be a positive Go duration.", name))
		}
	}
	return result
}

func operationTimeout(ctx context.Context, value types.Object, operation string) time.Duration {
	fallback := map[string]time.Duration{"create": time.Hour, "update": time.Hour, "delete": 30 * time.Minute}[operation]
	if value.IsNull() || value.IsUnknown() {
		return fallback
	}
	var timeouts timeoutModel
	if value.As(ctx, &timeouts, basetypes.ObjectAsOptions{}).HasError() {
		return fallback
	}
	field := map[string]types.String{"create": timeouts.Create, "update": timeouts.Update, "delete": timeouts.Delete}[operation]
	if field.IsNull() || field.IsUnknown() {
		return fallback
	}
	duration, err := time.ParseDuration(field.ValueString())
	if err != nil || duration <= 0 {
		return fallback
	}
	return duration
}
