package products

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/elva-labs/terraform-provider-vew/internal/providerdata"
	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	vewproducts "github.com/elva-labs/terraform-provider-vew/internal/vew/products"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	schema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

const defaultPromotionTimeout = time.Hour

var (
	_ resource.Resource                   = (*promotionResource)(nil)
	_ resource.ResourceWithConfigure      = (*promotionResource)(nil)
	_ resource.ResourceWithImportState    = (*promotionResource)(nil)
	_ resource.ResourceWithValidateConfig = (*promotionResource)(nil)
)

type promotionResource struct {
	client vewproducts.PromotionAPI
	waiter vew.Waiter
}

type promotionModel struct {
	ID            types.String `tfsdk:"id"`
	ProjectID     types.String `tfsdk:"project_id"`
	ProductID     types.String `tfsdk:"product_id"`
	VersionID     types.String `tfsdk:"version_id"`
	Stage         types.String `tfsdk:"stage"`
	VersionName   types.String `tfsdk:"version_name"`
	Status        types.String `tfsdk:"status"`
	Distributions types.List   `tfsdk:"distributions"`
	Timeouts      types.Object `tfsdk:"timeouts"`
}

type promotionTimeoutsModel struct {
	Create types.String `tfsdk:"create"`
}

var (
	promotionTimeoutTypes    = map[string]attr.Type{"create": types.StringType}
	distributionAttributeMap = map[string]attr.Type{"aws_account_id": types.StringType, "region": types.StringType, "status": types.StringType}
	distributionObjectType   = types.ObjectType{AttrTypes: distributionAttributeMap}
)

// NewProductVersionPromotionResource constructs the vew_product_version_promotion resource.
func NewProductVersionPromotionResource() resource.Resource { return &promotionResource{} }

func (r *promotionResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_product_version_promotion"
}

func (r *promotionResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{
				stringplanmodifier.UseStateForUnknown(),
			}},
			"project_id":   replacementString(),
			"product_id":   replacementString(),
			"version_id":   replacementString(),
			"stage":        replacementString(),
			"version_name": schema.StringAttribute{Computed: true},
			"status":       schema.StringAttribute{Computed: true},
			"distributions": schema.ListNestedAttribute{Computed: true, NestedObject: schema.NestedAttributeObject{
				Attributes: map[string]schema.Attribute{
					"aws_account_id": schema.StringAttribute{Computed: true},
					"region":         schema.StringAttribute{Computed: true},
					"status":         schema.StringAttribute{Computed: true},
				},
			}},
		},
		Blocks: map[string]schema.Block{
			"timeouts": schema.SingleNestedBlock{Attributes: map[string]schema.Attribute{
				"create": schema.StringAttribute{Optional: true},
			}},
		},
	}
}

func (r *promotionResource) Configure(_ context.Context, request resource.ConfigureRequest, response *resource.ConfigureResponse) {
	if request.ProviderData == nil {
		return
	}
	data, ok := request.ProviderData.(providerdata.Data)
	if !ok || strings.TrimSpace(data.PublishingAPIURL) == "" || data.Promotions == nil {
		response.Diagnostics.AddError("Missing Publishing API URL", "The vew_product_version_promotion resource requires a Publishing API endpoint. Set publishing_api_url or VEW_PUBLISHING_API_URL.")
		return
	}
	r.client, r.waiter = data.Promotions, data.Waiter
}

func (r *promotionResource) ValidateConfig(ctx context.Context, request resource.ValidateConfigRequest, response *resource.ValidateConfigResponse) {
	var config promotionModel
	response.Diagnostics.Append(request.Config.Get(ctx, &config)...)
	if response.Diagnostics.HasError() {
		return
	}
	for name, value := range map[string]types.String{"project_id": config.ProjectID, "product_id": config.ProductID, "version_id": config.VersionID} {
		if known(value) && strings.TrimSpace(value.ValueString()) == "" {
			response.Diagnostics.AddAttributeError(path.Root(name), "Empty promotion value", name+" must be non-empty.")
		}
	}
	if known(config.Stage) && !contains(vewproducts.Stages, config.Stage.ValueString()) {
		response.Diagnostics.AddAttributeError(path.Root("stage"), "Invalid stage", "stage must be DEV, QA, or PROD.")
	}
	if !config.Timeouts.IsNull() && !config.Timeouts.IsUnknown() {
		var timeouts promotionTimeoutsModel
		response.Diagnostics.Append(config.Timeouts.As(ctx, &timeouts, basetypes.ObjectAsOptions{})...)
		if known(timeouts.Create) {
			if duration, err := time.ParseDuration(timeouts.Create.ValueString()); err != nil || duration <= 0 {
				response.Diagnostics.AddAttributeError(path.Root("timeouts").AtName("create"), "Invalid promotion timeout", "timeouts.create must be a positive Go duration string.")
			}
		}
	}
}

func promotionID(projectID, productID, versionID, stage string) string {
	return strings.Join([]string{projectID, productID, versionID, stage}, "/")
}

func (r *promotionResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	parts := strings.Split(request.ID, "/")
	valid := len(parts) == 4
	for _, part := range parts {
		valid = valid && strings.TrimSpace(part) != "" && part == strings.TrimSpace(part)
	}
	if !valid || !contains(vewproducts.Stages, parts[3]) {
		response.Diagnostics.AddError("Invalid promotion import ID", "Expected project_id/product_id/version_id/stage with stage DEV, QA, or PROD.")
		return
	}
	for index, name := range []string{"project_id", "product_id", "version_id", "stage"} {
		response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root(name), parts[index])...)
	}
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), request.ID)...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("timeouts"), types.ObjectNull(promotionTimeoutTypes))...)
}

func (r *promotionResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	var model promotionModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &model)...)
	if response.Diagnostics.HasError() {
		return
	}
	projectID, productID, versionID, stage := model.ProjectID.ValueString(), model.ProductID.ValueString(), model.VersionID.ValueString(), model.Stage.ValueString()
	model.ID = types.StringValue(promotionID(projectID, productID, versionID, stage))
	stateCtx := context.WithoutCancel(ctx)
	var promotion vewproducts.Promotion
	// PUT is an upsert: repeating it while VEW publishes returns the current
	// state and never promotes twice.
	err := r.waiter.Until(ctx, promotionTimeout(ctx, model.Timeouts), 0, func(ctx context.Context) (vew.PollResult, error) {
		current, err := r.client.PromoteVersion(ctx, projectID, productID, versionID, stage)
		if err != nil {
			return vew.PollResult{}, err
		}
		promotion = current
		return vew.PollResult{Status: current.Status, RetryAfter: 30 * time.Second}, nil
	}, func(status string) (bool, error) {
		switch status {
		case "CREATED":
			return true, nil
		case "FAILED", "RETIRED":
			return true, &vew.TerminalStatusError{Status: status}
		}
		return false, nil
	})
	if promotion.VersionID != "" {
		// Keep what VEW accepted so a later apply reconciles instead of losing it.
		response.Diagnostics.Append(setPromotionState(ctx, &model, promotion)...)
		response.Diagnostics.Append(response.State.Set(stateCtx, &model)...)
	}
	if err != nil {
		var terminal *vew.TerminalStatusError
		var timeout *vew.TimeoutError
		switch {
		case errors.As(err, &terminal):
			response.Diagnostics.AddError("VEW product version promotion failed", fmt.Sprintf("Version %q reached status %s in stage %s. Inspect its distributions in VEW.", versionID, terminal.Status, stage))
		case errors.As(err, &timeout):
			response.Diagnostics.AddError("VEW product version promotion timed out", fmt.Sprintf("Version %q is still being published to stage %s; the promotion is retained in state. Apply again or raise timeouts.create.", versionID, stage))
		default:
			addPromotionError(&response.Diagnostics, "create", err, projectID)
		}
	}
}

func (r *promotionResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	var state promotionModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	promotion, err := r.client.GetPromotion(ctx, state.ProjectID.ValueString(), state.ProductID.ValueString(), state.VersionID.ValueString(), state.Stage.ValueString())
	if vew.IsNotFound(err) {
		response.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		addPromotionError(&response.Diagnostics, "read", err, state.ProjectID.ValueString())
		return
	}
	if promotion.VersionID != state.VersionID.ValueString() || promotion.ProductID != state.ProductID.ValueString() {
		response.Diagnostics.AddError("Invalid VEW promotion response", "The promotion response did not match the product and version identity in state.")
		return
	}
	state.ID = types.StringValue(promotionID(state.ProjectID.ValueString(), state.ProductID.ValueString(), state.VersionID.ValueString(), state.Stage.ValueString()))
	response.Diagnostics.Append(setPromotionState(ctx, &state, promotion)...)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

// Update is only reached for timeouts; every identifying attribute forces replacement.
func (r *promotionResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan, state promotionModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	state.Timeouts = plan.Timeouts
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

func (r *promotionResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	var state promotionModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	if err := r.client.ForgetPromotion(ctx, state.ProjectID.ValueString(), state.ProductID.ValueString(), state.VersionID.ValueString(), state.Stage.ValueString()); err != nil {
		addPromotionError(&response.Diagnostics, "delete", err, state.ProjectID.ValueString())
	}
}

func promotionTimeout(ctx context.Context, timeouts types.Object) time.Duration {
	if timeouts.IsNull() || timeouts.IsUnknown() {
		return defaultPromotionTimeout
	}
	var values promotionTimeoutsModel
	if diagnostics := timeouts.As(ctx, &values, basetypes.ObjectAsOptions{}); diagnostics.HasError() || !known(values.Create) {
		return defaultPromotionTimeout
	}
	duration, err := time.ParseDuration(values.Create.ValueString())
	if err != nil || duration <= 0 {
		return defaultPromotionTimeout
	}
	return duration
}

func setPromotionState(ctx context.Context, model *promotionModel, promotion vewproducts.Promotion) diag.Diagnostics {
	model.VersionName = types.StringValue(promotion.VersionName)
	model.Status = types.StringValue(promotion.Status)
	distributions := make([]attr.Value, 0, len(promotion.Distributions))
	var diagnostics diag.Diagnostics
	for _, distribution := range promotion.Distributions {
		value, valueDiagnostics := types.ObjectValue(distributionAttributeMap, map[string]attr.Value{
			"aws_account_id": types.StringValue(distribution.AWSAccountID),
			"region":         types.StringValue(distribution.Region),
			"status":         types.StringValue(distribution.Status),
		})
		diagnostics.Append(valueDiagnostics...)
		distributions = append(distributions, value)
	}
	list, listDiagnostics := types.ListValue(distributionObjectType, distributions)
	diagnostics.Append(listDiagnostics...)
	model.Distributions = list
	if model.Timeouts.IsUnknown() {
		model.Timeouts = types.ObjectNull(promotionTimeoutTypes)
	}
	return diagnostics
}

func addPromotionError(diagnostics *diag.Diagnostics, operation string, err error, projectID string) {
	message := "VEW Publishing product version promotion " + operation + " failed"
	var apiError *vew.APIError
	if !errors.As(err, &apiError) {
		diagnostics.AddError(message, "The Publishing API request could not be completed safely.")
		return
	}
	switch {
	case apiError.Status == 403:
		scope := "version.read"
		if operation != "read" {
			scope = "version.promote"
		}
		diagnostics.AddError(message, fmt.Sprintf("The service client needs Publishing scope %q and an active assignment to project %q.", scope, projectID))
		return
	case apiError.Status == 404 && operation == "create":
		diagnostics.AddError(message, fmt.Sprintf("The product or version does not exist in project %q.", projectID))
		return
	case apiError.Status == 422:
		diagnostics.AddError(message, "VEW rejected the promotion: every distribution of the version must be created, PROD accepts only release candidates, and the stage needs an onboarded account.")
		return
	}
	parts := []string{fmt.Sprintf("HTTP status %d", apiError.Status)}
	if safeProductCorrelation.MatchString(apiError.Problem.Code) {
		parts = append(parts, "problem code "+apiError.Problem.Code)
	}
	if safeProductCorrelation.MatchString(apiError.Problem.RequestID) {
		parts = append(parts, "request ID "+apiError.Problem.RequestID)
	}
	diagnostics.AddError(message, strings.Join(parts, ", "))
}
