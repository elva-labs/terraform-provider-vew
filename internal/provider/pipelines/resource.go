package pipelines

import (
	"context"
	"errors"
	"strings"

	"github.com/elva-labs/terraform-provider-vew/internal/providerdata"
	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	vewpipelines "github.com/elva-labs/terraform-provider-vew/internal/vew/pipelines"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                   = (*pipelineResource)(nil)
	_ resource.ResourceWithConfigure      = (*pipelineResource)(nil)
	_ resource.ResourceWithValidateConfig = (*pipelineResource)(nil)
	_ resource.ResourceWithImportState    = (*pipelineResource)(nil)
)

type pipelineResource struct {
	client vewpipelines.API
	waiter vew.Waiter
}

func NewPipelineResource() resource.Resource { return &pipelineResource{} }

func (r *pipelineResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_pipeline"
}

func (r *pipelineResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = pipelineSchema()
}

func (r *pipelineResource) Configure(_ context.Context, request resource.ConfigureRequest, response *resource.ConfigureResponse) {
	if request.ProviderData == nil {
		return
	}
	data, ok := request.ProviderData.(providerdata.Data)
	if !ok || data.Pipelines == nil || data.Waiter == nil {
		response.Diagnostics.AddError("Missing Pipeline API", "Expected provider data to include a Pipelines API and Waiter.")
		return
	}
	r.client, r.waiter = data.Pipelines, data.Waiter
}

func (r *pipelineResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	parts := strings.Split(request.ID, "/")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" || parts[0] != strings.TrimSpace(parts[0]) || parts[1] != strings.TrimSpace(parts[1]) {
		response.Diagnostics.AddError("Invalid pipeline import ID", "Expected project_id/pipeline_id.")
		return
	}
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("project_id"), parts[0])...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
}

func (r *pipelineResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	var model pipelineModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &model)...)
	if response.Diagnostics.HasError() {
		return
	}
	instanceTypes, err := configuredInstanceTypes(ctx, model.BuildInstanceTypes)
	if err != nil {
		addPipelineError(&response.Diagnostics, "create", err, "", model.RecipeID.ValueString(), model.RecipeVersionID.ValueString())
		return
	}
	input := vewpipelines.CreatePipelineInput{Name: model.Name.ValueString(), Description: model.Description.ValueString(), RecipeID: model.RecipeID.ValueString(), RecipeVersionID: model.RecipeVersionID.ValueString(), BuildInstanceTypes: instanceTypes, Schedule: model.Schedule.ValueString(), ProductID: productPointer(model.ProductID)}
	timeout := operationTimeout(ctx, model.Timeouts, "create")
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	action, err := r.client.CreatePipeline(ctx, model.ProjectID.ValueString(), input)
	if err != nil {
		addPipelineError(&response.Diagnostics, "create", err, "", model.RecipeID.ValueString(), model.RecipeVersionID.ValueString())
		return
	}
	model.ID = types.StringValue(action.ID)
	model.Status = types.StringValue("CREATING")
	nullRemoteMetadata(&model)
	stateCtx := context.WithoutCancel(ctx)
	response.Diagnostics.Append(response.State.Set(stateCtx, &model)...)
	if response.Diagnostics.HasError() {
		return
	}
	err = r.waitPipeline(ctx, &model, timeout, action.RetryAfter, createdStatus, false)
	response.Diagnostics.Append(response.State.Set(stateCtx, &model)...)
	if err != nil {
		addPipelineError(&response.Diagnostics, "create", err, model.ID.ValueString(), model.RecipeID.ValueString(), model.RecipeVersionID.ValueString())
	}
}

func (r *pipelineResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	var model pipelineModel
	response.Diagnostics.Append(request.State.Get(ctx, &model)...)
	if response.Diagnostics.HasError() {
		return
	}
	remote, err := r.client.GetPipeline(ctx, model.ProjectID.ValueString(), model.ID.ValueString())
	if vew.IsNotFound(err) || (err == nil && remote.Status == "RETIRED") {
		response.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		addPipelineError(&response.Diagnostics, "read", err, model.ID.ValueString(), model.RecipeID.ValueString(), model.RecipeVersionID.ValueString())
		return
	}
	prior := model
	if err := setPipelineState(ctx, &model, remote); err != nil {
		addPipelineError(&response.Diagnostics, "read", err, model.ID.ValueString(), model.RecipeID.ValueString(), model.RecipeVersionID.ValueString())
		return
	}
	if request.Private != nil {
		retry, diagnostics := request.Private.GetKey(ctx, updateRetryKey)
		response.Diagnostics.Append(diagnostics...)
		if string(retry) == "true" {
			if remote.Status == "CREATED" && equivalentMutable(model, prior) {
				if response.Private != nil {
					response.Diagnostics.Append(response.Private.SetKey(ctx, updateRetryKey, nil)...)
				}
			} else {
				preserveMutable(&model, prior)
			}
		}
	}
	response.Diagnostics.Append(response.State.Set(ctx, &model)...)
}

func configuredInstanceTypes(ctx context.Context, list types.List) ([]string, error) {
	var values []string
	if diagnostics := list.ElementsAs(ctx, &values, false); diagnostics.HasError() {
		return nil, errors.New("invalid build instance types")
	}
	return values, nil
}

func productPointer(value types.String) *string {
	if value.IsNull() {
		return nil
	}
	product := value.ValueString()
	return &product
}

func nullableString(value *string) types.String {
	if value == nil {
		return types.StringNull()
	}
	return types.StringValue(*value)
}

func nullRemoteMetadata(model *pipelineModel) {
	model.RecipeName, model.RecipeVersionName, model.DistributionConfigARN, model.InfrastructureConfigARN, model.PipelineARN = types.StringNull(), types.StringNull(), types.StringNull(), types.StringNull(), types.StringNull()
	model.CreatedAt, model.CreatedBy, model.UpdatedAt, model.UpdatedBy = types.StringNull(), types.StringNull(), types.StringNull(), types.StringNull()
}

func setPipelineState(ctx context.Context, model *pipelineModel, remote vewpipelines.Pipeline) error {
	if remote.ID == "" {
		return errors.New("pipeline response missing ID")
	}
	if remote.ProjectID != "" && !model.ProjectID.IsNull() && !model.ProjectID.IsUnknown() && remote.ProjectID != model.ProjectID.ValueString() {
		return errors.New("pipeline response project ID differs from requested project")
	}
	if remote.ProjectID != "" {
		model.ProjectID = types.StringValue(remote.ProjectID)
	}
	instances, diagnostics := types.ListValueFrom(ctx, types.StringType, remote.BuildInstanceTypes)
	if diagnostics.HasError() {
		return errors.New("pipeline response has invalid build instance types")
	}
	model.ID = types.StringValue(remote.ID)
	model.Name = types.StringValue(remote.Name)
	model.Description = types.StringValue(remote.Description)
	model.RecipeID = types.StringValue(remote.RecipeID)
	model.RecipeName = types.StringValue(remote.RecipeName)
	model.RecipeVersionID = types.StringValue(remote.RecipeVersionID)
	model.RecipeVersionName = types.StringValue(remote.RecipeVersionName)
	model.BuildInstanceTypes = instances
	model.Schedule = types.StringValue(remote.Schedule)
	model.ProductID = nullableString(remote.ProductID)
	model.Status = types.StringValue(remote.Status)
	model.DistributionConfigARN = nullableString(remote.DistributionConfigARN)
	model.InfrastructureConfigARN = nullableString(remote.InfrastructureConfigARN)
	model.PipelineARN = nullableString(remote.PipelineARN)
	model.CreatedAt = types.StringValue(remote.CreatedAt)
	model.CreatedBy = types.StringValue(remote.CreatedBy)
	model.UpdatedAt = types.StringValue(remote.UpdatedAt)
	model.UpdatedBy = types.StringValue(remote.UpdatedBy)
	return nil
}
