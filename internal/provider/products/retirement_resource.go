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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

const (
	defaultRetirementTimeout = 30 * time.Minute
	maxRetireReasonLength    = 500
)

var (
	_ resource.Resource                   = (*retirementResource)(nil)
	_ resource.ResourceWithConfigure      = (*retirementResource)(nil)
	_ resource.ResourceWithImportState    = (*retirementResource)(nil)
	_ resource.ResourceWithValidateConfig = (*retirementResource)(nil)
)

type retirementResource struct {
	client vewproducts.RetirementAPI
	waiter vew.Waiter
}

type retirementModel struct {
	ID            types.String `tfsdk:"id"`
	ProjectID     types.String `tfsdk:"project_id"`
	ProductID     types.String `tfsdk:"product_id"`
	VersionID     types.String `tfsdk:"version_id"`
	Reason        types.String `tfsdk:"reason"`
	Force         types.Bool   `tfsdk:"force"`
	VersionName   types.String `tfsdk:"version_name"`
	Status        types.String `tfsdk:"status"`
	Distributions types.List   `tfsdk:"distributions"`
	Timeouts      types.Object `tfsdk:"timeouts"`
}

// NewProductVersionRetirementResource constructs the vew_product_version_retirement resource.
func NewProductVersionRetirementResource() resource.Resource { return &retirementResource{} }

func (r *retirementResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_product_version_retirement"
}

func (r *retirementResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{
				stringplanmodifier.UseStateForUnknown(),
			}},
			"project_id": replacementString(),
			"product_id": replacementString(),
			"version_id": replacementString(),
			// The reason and force only matter when the retirement starts; changing them later changes nothing in VEW.
			"reason": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("")},
			"force":  schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false)},
			"version_name": schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{
				stringplanmodifier.UseStateForUnknown(),
			}},
			"status": schema.StringAttribute{Computed: true},
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

func (r *retirementResource) Configure(_ context.Context, request resource.ConfigureRequest, response *resource.ConfigureResponse) {
	if request.ProviderData == nil {
		return
	}
	data, ok := request.ProviderData.(providerdata.Data)
	if !ok || strings.TrimSpace(data.PublishingAPIURL) == "" || data.Retirements == nil {
		response.Diagnostics.AddError("Missing Publishing API URL", "The vew_product_version_retirement resource requires a Publishing API endpoint. Set publishing_api_url or VEW_PUBLISHING_API_URL.")
		return
	}
	r.client, r.waiter = data.Retirements, data.Waiter
}

func (r *retirementResource) ValidateConfig(ctx context.Context, request resource.ValidateConfigRequest, response *resource.ValidateConfigResponse) {
	var config retirementModel
	response.Diagnostics.Append(request.Config.Get(ctx, &config)...)
	if response.Diagnostics.HasError() {
		return
	}
	for name, value := range map[string]types.String{"project_id": config.ProjectID, "product_id": config.ProductID, "version_id": config.VersionID} {
		if known(value) && strings.TrimSpace(value.ValueString()) == "" {
			response.Diagnostics.AddAttributeError(path.Root(name), "Empty retirement value", name+" must be non-empty.")
		}
	}
	if known(config.Reason) && len(config.Reason.ValueString()) > maxRetireReasonLength {
		response.Diagnostics.AddAttributeError(path.Root("reason"), "Retirement reason too long", fmt.Sprintf("reason must be at most %d characters.", maxRetireReasonLength))
	}
	if !config.Timeouts.IsNull() && !config.Timeouts.IsUnknown() {
		var timeouts promotionTimeoutsModel
		response.Diagnostics.Append(config.Timeouts.As(ctx, &timeouts, basetypes.ObjectAsOptions{})...)
		if known(timeouts.Create) {
			if duration, err := time.ParseDuration(timeouts.Create.ValueString()); err != nil || duration <= 0 {
				response.Diagnostics.AddAttributeError(path.Root("timeouts").AtName("create"), "Invalid retirement timeout", "timeouts.create must be a positive Go duration string.")
			}
		}
	}
}

func retirementID(projectID, productID, versionID string) string {
	return strings.Join([]string{projectID, productID, versionID}, "/")
}

func (r *retirementResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	parts := strings.Split(request.ID, "/")
	valid := len(parts) == 3
	for _, part := range parts {
		valid = valid && strings.TrimSpace(part) != "" && part == strings.TrimSpace(part)
	}
	if !valid {
		response.Diagnostics.AddError("Invalid retirement import ID", "Expected project_id/product_id/version_id.")
		return
	}
	for index, name := range []string{"project_id", "product_id", "version_id"} {
		response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root(name), parts[index])...)
	}
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), request.ID)...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("reason"), "")...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("force"), false)...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("timeouts"), types.ObjectNull(promotionTimeoutTypes))...)
}

func (r *retirementResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	var model retirementModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &model)...)
	if response.Diagnostics.HasError() {
		return
	}
	projectID, productID, versionID := model.ProjectID.ValueString(), model.ProductID.ValueString(), model.VersionID.ValueString()
	model.ID = types.StringValue(retirementID(projectID, productID, versionID))
	retire := vewproducts.RetireRequest{Reason: model.Reason.ValueString(), Force: model.Force.ValueBool()}
	stateCtx := context.WithoutCancel(ctx)
	var retirement vewproducts.Retirement
	// PUT is an upsert: repeating it while VEW retires returns the current
	// state and never retires twice.
	err := r.waiter.Until(ctx, promotionTimeout(ctx, model.Timeouts, defaultRetirementTimeout), 0, func(ctx context.Context) (vew.PollResult, error) {
		current, err := r.client.RetireVersion(ctx, projectID, productID, versionID, retire)
		if err != nil {
			return vew.PollResult{}, err
		}
		retirement = current
		return vew.PollResult{Status: current.Status, RetryAfter: 30 * time.Second}, nil
	}, func(status string) (bool, error) {
		return status == "RETIRED", nil
	})
	if retirement.VersionID != "" {
		// Keep what VEW accepted so a later apply reconciles instead of losing it.
		response.Diagnostics.Append(setRetirementState(ctx, &model, retirement)...)
		response.Diagnostics.Append(response.State.Set(stateCtx, &model)...)
	}
	if err != nil {
		var timeout *vew.TimeoutError
		if errors.As(err, &timeout) {
			response.Diagnostics.AddError("VEW product version retirement timed out", fmt.Sprintf("Version %q is still being retired; the retirement is retained in state. Apply again or raise timeouts.create.", versionID))
			return
		}
		addRetirementError(&response.Diagnostics, "create", err, projectID)
	}
}

func (r *retirementResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	var state retirementModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	retirement, err := r.client.GetRetirement(ctx, state.ProjectID.ValueString(), state.ProductID.ValueString(), state.VersionID.ValueString())
	if vew.IsNotFound(err) {
		// Restored in the portal (or never retired): the next apply retires it again.
		response.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		addRetirementError(&response.Diagnostics, "read", err, state.ProjectID.ValueString())
		return
	}
	if retirement.VersionID != state.VersionID.ValueString() || retirement.ProductID != state.ProductID.ValueString() {
		response.Diagnostics.AddError("Invalid VEW retirement response", "The retirement response did not match the product and version identity in state.")
		return
	}
	state.ID = types.StringValue(retirementID(state.ProjectID.ValueString(), state.ProductID.ValueString(), state.VersionID.ValueString()))
	response.Diagnostics.Append(setRetirementState(ctx, &state, retirement)...)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

// Update only records reason, force and timeouts; the retirement itself does not change.
func (r *retirementResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan, state retirementModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	state.Reason, state.Force, state.Timeouts = plan.Reason, plan.Force, plan.Timeouts
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

func (r *retirementResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	var state retirementModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	if err := r.client.ForgetRetirement(ctx, state.ProjectID.ValueString(), state.ProductID.ValueString(), state.VersionID.ValueString()); err != nil {
		addRetirementError(&response.Diagnostics, "delete", err, state.ProjectID.ValueString())
	}
}

func setRetirementState(ctx context.Context, model *retirementModel, retirement vewproducts.Retirement) diag.Diagnostics {
	model.VersionName = types.StringValue(retirement.VersionName)
	model.Status = types.StringValue(retirement.Status)
	distributions := make([]attr.Value, 0, len(retirement.Distributions))
	var diagnostics diag.Diagnostics
	for _, distribution := range retirement.Distributions {
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

func addRetirementError(diagnostics *diag.Diagnostics, operation string, err error, projectID string) {
	message := "VEW Publishing product version retirement " + operation + " failed"
	var apiError *vew.APIError
	if !errors.As(err, &apiError) {
		diagnostics.AddError(message, "The Publishing API request could not be completed safely.")
		return
	}
	switch {
	case apiError.Status == 403:
		scope := "version.read"
		if operation != "read" {
			scope = "version.retire"
		}
		diagnostics.AddError(message, fmt.Sprintf("The service client needs Publishing scope %q and an active assignment to project %q.", scope, projectID))
		return
	case apiError.Status == 404 && operation == "create":
		diagnostics.AddError(message, fmt.Sprintf("The product or version does not exist in project %q.", projectID))
		return
	case apiError.Status == 409 && apiError.Problem.Code == "VERSION_IN_USE":
		diagnostics.AddError(message, "Workbenches are still on this version. Retired, they keep running but can no longer change size until their owners update. Update or remove them first, or set force = true.")
		return
	case apiError.Status == 422:
		diagnostics.AddError(message, "VEW rejected the retirement: the product's latest version can't be retired, and every distribution of the version must be created.")
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
