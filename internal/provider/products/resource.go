package products

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

const (
	productCreateKey     = "product_create_idempotency_key"
	defaultDeleteTimeout = 30 * time.Minute
)

var (
	safeProductCorrelation = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
	// VEW's product name and description rules; checked at plan time so a bad
	// value fails before anything is created.
	productNamePattern        = regexp.MustCompile(`^[A-Za-z0-9_ -]{1,50}$`)
	productDescriptionPattern = regexp.MustCompile(`^[A-Za-z0-9_ -]{0,100}$`)
	productTypes              = []string{"WORKBENCH", "VIRTUAL_TARGET", "CONTAINER"}
)

var (
	_ resource.Resource                   = (*productResource)(nil)
	_ resource.ResourceWithConfigure      = (*productResource)(nil)
	_ resource.ResourceWithImportState    = (*productResource)(nil)
	_ resource.ResourceWithValidateConfig = (*productResource)(nil)
)

type productResource struct {
	client vewproducts.API
	waiter vew.Waiter
}

type productModel struct {
	ID                   types.String `tfsdk:"id"`
	ProjectID            types.String `tfsdk:"project_id"`
	Name                 types.String `tfsdk:"name"`
	Description          types.String `tfsdk:"description"`
	Type                 types.String `tfsdk:"type"`
	TechnologyID         types.String `tfsdk:"technology_id"`
	TechnologyName       types.String `tfsdk:"technology_name"`
	Status               types.String `tfsdk:"status"`
	RecommendedVersionID types.String `tfsdk:"recommended_version_id"`
	AvailableStages      types.List   `tfsdk:"available_stages"`
	CreatedAt            types.String `tfsdk:"created_at"`
	UpdatedAt            types.String `tfsdk:"updated_at"`
	Timeouts             types.Object `tfsdk:"timeouts"`
}

type timeoutsModel struct {
	Delete types.String `tfsdk:"delete"`
}

var timeoutAttributeTypes = map[string]attr.Type{"delete": types.StringType}

// NewProductResource constructs the vew_product resource.
func NewProductResource() resource.Resource { return &productResource{} }

func (r *productResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_product"
}

func replacementString() schema.StringAttribute {
	return schema.StringAttribute{Required: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}}
}

func (r *productResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{
				stringplanmodifier.UseStateForUnknown(),
			}},
			"project_id":    replacementString(),
			"name":          schema.StringAttribute{Required: true},
			"description":   schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("")},
			"type":          replacementString(),
			"technology_id": replacementString(),
			"technology_name": schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{
				stringplanmodifier.UseStateForUnknown(),
			}},
			"status":                 schema.StringAttribute{Computed: true},
			"recommended_version_id": schema.StringAttribute{Computed: true},
			"available_stages":       schema.ListAttribute{Computed: true, ElementType: types.StringType},
			"created_at": schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{
				stringplanmodifier.UseStateForUnknown(),
			}},
			"updated_at": schema.StringAttribute{Computed: true},
		},
		Blocks: map[string]schema.Block{
			"timeouts": schema.SingleNestedBlock{Attributes: map[string]schema.Attribute{
				"delete": schema.StringAttribute{Optional: true},
			}},
		},
	}
}

func (r *productResource) Configure(_ context.Context, request resource.ConfigureRequest, response *resource.ConfigureResponse) {
	if request.ProviderData == nil {
		return
	}
	data, ok := request.ProviderData.(providerdata.Data)
	if !ok || strings.TrimSpace(data.PublishingAPIURL) == "" || data.Products == nil {
		response.Diagnostics.AddError("Missing Publishing API URL", "The vew_product resource requires a Publishing API endpoint. Set publishing_api_url or VEW_PUBLISHING_API_URL.")
		return
	}
	r.client, r.waiter = data.Products, data.Waiter
}

func (r *productResource) ValidateConfig(ctx context.Context, request resource.ValidateConfigRequest, response *resource.ValidateConfigResponse) {
	var config productModel
	response.Diagnostics.Append(request.Config.Get(ctx, &config)...)
	if response.Diagnostics.HasError() {
		return
	}
	for name, value := range map[string]types.String{"project_id": config.ProjectID, "technology_id": config.TechnologyID} {
		if known(value) && strings.TrimSpace(value.ValueString()) == "" {
			response.Diagnostics.AddAttributeError(path.Root(name), "Empty product value", name+" must be non-empty.")
		}
	}
	if known(config.Name) && !productNamePattern.MatchString(config.Name.ValueString()) {
		response.Diagnostics.AddAttributeError(path.Root("name"), "Invalid product name", "name must be 1 to 50 letters, digits, spaces, hyphens, or underscores.")
	}
	if known(config.Description) && !productDescriptionPattern.MatchString(config.Description.ValueString()) {
		response.Diagnostics.AddAttributeError(path.Root("description"), "Invalid product description", "description must be at most 100 letters, digits, spaces, hyphens, or underscores.")
	}
	if known(config.Type) && !contains(productTypes, config.Type.ValueString()) {
		response.Diagnostics.AddAttributeError(path.Root("type"), "Invalid product type", "type must be one of "+strings.Join(productTypes, ", ")+".")
	}
	if !config.Timeouts.IsNull() && !config.Timeouts.IsUnknown() {
		var timeouts timeoutsModel
		response.Diagnostics.Append(config.Timeouts.As(ctx, &timeouts, basetypes.ObjectAsOptions{})...)
		if known(timeouts.Delete) {
			if duration, err := time.ParseDuration(timeouts.Delete.ValueString()); err != nil || duration <= 0 {
				response.Diagnostics.AddAttributeError(path.Root("timeouts").AtName("delete"), "Invalid product timeout", "timeouts.delete must be a positive Go duration string.")
			}
		}
	}
}

func known(value types.String) bool { return !value.IsNull() && !value.IsUnknown() }

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func (r *productResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	parts := strings.Split(request.ID, "/")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" || parts[0] != strings.TrimSpace(parts[0]) || parts[1] != strings.TrimSpace(parts[1]) {
		response.Diagnostics.AddError("Invalid product import ID", "Expected project_id/product_id.")
		return
	}
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("project_id"), parts[0])...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("timeouts"), types.ObjectNull(timeoutAttributeTypes))...)
}

func (r *productResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	var model productModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &model)...)
	if response.Diagnostics.HasError() {
		return
	}
	key, err := newIdempotencyKey()
	if err != nil {
		response.Diagnostics.AddError("Unable to create VEW product", "A stable create idempotency key could not be generated.")
		return
	}
	if response.Private != nil {
		privateKey, marshalErr := json.Marshal(key)
		if marshalErr != nil {
			response.Diagnostics.AddError("Unable to create VEW product", "The create idempotency key could not be preserved safely.")
			return
		}
		response.Diagnostics.Append(response.Private.SetKey(ctx, productCreateKey, privateKey)...)
		if response.Diagnostics.HasError() {
			return
		}
	}
	input := vewproducts.CreateProductInput{
		Name: model.Name.ValueString(), Type: model.Type.ValueString(),
		Description: model.Description.ValueString(), TechnologyID: model.TechnologyID.ValueString(),
	}
	id, err := r.client.CreateProduct(ctx, model.ProjectID.ValueString(), input, key)
	if err != nil && ambiguousCreate(err) {
		// The idempotency reservation makes a second identical submission safe
		// when the first response was lost.
		id, err = r.client.CreateProduct(ctx, model.ProjectID.ValueString(), input, key)
	}
	if err != nil {
		addProductError(&response.Diagnostics, "create", err, model.ProjectID.ValueString())
		return
	}
	model.ID = types.StringValue(id)
	stateCtx := context.WithoutCancel(ctx)
	remote, err := r.client.GetProduct(ctx, model.ProjectID.ValueString(), id)
	if err == nil {
		err = validateIdentity(remote, model.ProjectID.ValueString(), id)
	}
	if err != nil {
		clearComputed(&model)
		response.Diagnostics.Append(response.State.Set(stateCtx, &model)...)
		response.Diagnostics.AddError("Unable to read VEW product after create", fmt.Sprintf("Product %q was accepted and its ID is retained in state, but canonical state could not be read. Refresh it on the next Terraform operation.", id))
		return
	}
	response.Diagnostics.Append(setState(ctx, &model, remote)...)
	response.Diagnostics.Append(response.State.Set(stateCtx, &model)...)
	if response.Private != nil {
		response.Diagnostics.Append(response.Private.SetKey(stateCtx, productCreateKey, nil)...)
	}
}

func (r *productResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	var state productModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	r.read(ctx, &state, &response.State, &response.Diagnostics)
}

func (r *productResource) read(ctx context.Context, state *productModel, terraformState *tfsdk.State, diagnostics *diag.Diagnostics) {
	remote, err := r.client.GetProduct(ctx, state.ProjectID.ValueString(), state.ID.ValueString())
	// VEW keeps returning archived products; they are gone for Terraform.
	if vew.IsNotFound(err) || (err == nil && remote.Archived()) {
		terraformState.RemoveResource(ctx)
		return
	}
	if err != nil {
		addProductError(diagnostics, "read", err, state.ProjectID.ValueString())
		return
	}
	if validateIdentity(remote, state.ProjectID.ValueString(), state.ID.ValueString()) != nil {
		diagnostics.AddError("Invalid VEW product response", "The product response did not match the project and product identity in state.")
		return
	}
	diagnostics.Append(setState(ctx, state, remote)...)
	diagnostics.Append(terraformState.Set(ctx, state)...)
}

func (r *productResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan, state productModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	input := vewproducts.UpdateProductInput{Name: plan.Name.ValueString(), Description: plan.Description.ValueString()}
	if err := r.client.UpdateProduct(ctx, state.ProjectID.ValueString(), state.ID.ValueString(), input); err != nil {
		if vew.IsNotFound(err) {
			response.State.RemoveResource(ctx)
			return
		}
		addProductError(&response.Diagnostics, "update", err, state.ProjectID.ValueString())
		return
	}
	plan.ID, plan.ProjectID = state.ID, state.ProjectID
	r.read(ctx, &plan, &response.State, &response.Diagnostics)
}

func (r *productResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	var state productModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	projectID, productID := state.ProjectID.ValueString(), state.ID.ValueString()
	// Archiving unpublishes every version of the product. DELETE is repeated
	// while VEW reports it as archiving; each repeat is a no-op on VEW's side.
	err := r.waiter.Until(ctx, deleteTimeout(ctx, state.Timeouts), 0, func(ctx context.Context) (vew.PollResult, error) {
		done, retryAfter, err := r.client.ArchiveProduct(ctx, projectID, productID)
		if err != nil {
			return vew.PollResult{}, err
		}
		if done {
			return vew.PollResult{Status: "ARCHIVED"}, nil
		}
		return vew.PollResult{Status: "ARCHIVING", RetryAfter: retryAfter}, nil
	}, func(status string) (bool, error) { return status == "ARCHIVED", nil })
	if err != nil {
		var timeout *vew.TimeoutError
		if errors.As(err, &timeout) {
			response.Diagnostics.AddError("VEW product delete timed out", fmt.Sprintf("Product %q is still archiving. Run terraform destroy again or raise timeouts.delete.", productID))
			return
		}
		addProductError(&response.Diagnostics, "delete", err, projectID)
	}
}

func deleteTimeout(ctx context.Context, timeouts types.Object) time.Duration {
	if timeouts.IsNull() || timeouts.IsUnknown() {
		return defaultDeleteTimeout
	}
	var values timeoutsModel
	if diagnostics := timeouts.As(ctx, &values, basetypes.ObjectAsOptions{}); diagnostics.HasError() || !known(values.Delete) {
		return defaultDeleteTimeout
	}
	duration, err := time.ParseDuration(values.Delete.ValueString())
	if err != nil || duration <= 0 {
		return defaultDeleteTimeout
	}
	return duration
}

func validateIdentity(remote vewproducts.Product, projectID, productID string) error {
	if strings.TrimSpace(remote.ID) == "" || remote.ID != productID || remote.ProjectID != projectID {
		return errors.New("product identity mismatch")
	}
	return nil
}

func clearComputed(model *productModel) {
	model.TechnologyName, model.Status, model.RecommendedVersionID = types.StringNull(), types.StringNull(), types.StringNull()
	model.AvailableStages = types.ListNull(types.StringType)
	model.CreatedAt, model.UpdatedAt = types.StringNull(), types.StringNull()
}

func setState(ctx context.Context, model *productModel, remote vewproducts.Product) diag.Diagnostics {
	model.ID = types.StringValue(remote.ID)
	model.ProjectID = types.StringValue(remote.ProjectID)
	model.Name = types.StringValue(remote.Name)
	model.Description = types.StringValue(remote.Description)
	model.Type = types.StringValue(remote.Type)
	model.TechnologyID = types.StringValue(remote.TechnologyID)
	model.TechnologyName = types.StringValue(remote.TechnologyName)
	model.Status = types.StringValue(remote.Status)
	model.RecommendedVersionID = types.StringNull()
	if remote.RecommendedVersionID != "" {
		model.RecommendedVersionID = types.StringValue(remote.RecommendedVersionID)
	}
	stages := remote.AvailableStages
	if stages == nil {
		stages = []string{}
	}
	var diagnostics diag.Diagnostics
	model.AvailableStages, diagnostics = types.ListValueFrom(ctx, types.StringType, stages)
	model.CreatedAt = types.StringValue(remote.CreatedAt)
	model.UpdatedAt = types.StringValue(remote.UpdatedAt)
	if model.Timeouts.IsUnknown() {
		model.Timeouts = types.ObjectNull(timeoutAttributeTypes)
	}
	return diagnostics
}

func ambiguousCreate(err error) bool {
	var apiError *vew.APIError
	if !errors.As(err, &apiError) {
		return true
	}
	return apiError.Status == 429 || apiError.Status >= 500 || (apiError.Status == 409 && apiError.Problem.Code == "IDEMPOTENCY_REQUEST_IN_PROGRESS")
}

func newIdempotencyKey() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	raw[6] = raw[6]&0x0f | 0x40
	raw[8] = raw[8]&0x3f | 0x80
	encoded := hex.EncodeToString(raw[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}

func addProductError(diagnostics *diag.Diagnostics, operation string, err error, projectID string) {
	message := "VEW Publishing product " + operation + " failed"
	var apiError *vew.APIError
	if !errors.As(err, &apiError) {
		diagnostics.AddError(message, "The Publishing API request could not be completed safely.")
		return
	}
	switch {
	case apiError.Status == 403:
		scope := "product.read"
		if operation != "read" {
			scope = "product.write"
		}
		diagnostics.AddError(message, fmt.Sprintf("The service client needs Publishing scope %q and an active assignment to project %q.", scope, projectID))
		return
	case apiError.Status == 422 && apiError.Problem.Code == "TECHNOLOGY_NOT_FOUND":
		diagnostics.AddError(message, fmt.Sprintf("technology_id does not name a technology of project %q.", projectID))
		return
	case apiError.Status == 409 && operation == "update":
		diagnostics.AddError(message, "The product is archived and can no longer be changed.")
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
