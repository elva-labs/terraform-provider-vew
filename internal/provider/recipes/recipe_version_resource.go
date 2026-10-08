package recipes

import (
	"context"

	"github.com/elva-labs/terraform-provider-vew/internal/providerdata"
	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	vewcomponents "github.com/elva-labs/terraform-provider-vew/internal/vew/components"
	vewrecipes "github.com/elva-labs/terraform-provider-vew/internal/vew/recipes"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                   = (*recipeVersionResource)(nil)
	_ resource.ResourceWithConfigure      = (*recipeVersionResource)(nil)
	_ resource.ResourceWithValidateConfig = (*recipeVersionResource)(nil)
	_ resource.ResourceWithImportState    = (*recipeVersionResource)(nil)
	_ resource.ResourceWithModifyPlan     = (*recipeVersionResource)(nil)
)

type recipeVersionResource struct {
	client            vewrecipes.RecipeVersionAPI
	components        vewcomponents.API
	componentVersions vewcomponents.ComponentVersionAPI
	waiter            vew.Waiter
}

func NewRecipeVersionResource() resource.Resource { return &recipeVersionResource{} }

func (r *recipeVersionResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_recipe_version"
}

func (r *recipeVersionResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = recipeVersionSchema()
}

func (r *recipeVersionResource) Configure(_ context.Context, request resource.ConfigureRequest, response *resource.ConfigureResponse) {
	if request.ProviderData == nil {
		return
	}
	data, ok := request.ProviderData.(providerdata.Data)
	if !ok || data.RecipeVersions == nil || data.Waiter == nil {
		response.Diagnostics.AddError("Missing Recipe Version API", "Expected provider data to include a RecipeVersions API and Waiter.")
		return
	}
	r.client, r.components, r.componentVersions, r.waiter = data.RecipeVersions, data.Components, data.ComponentVersions, data.Waiter
}

func (r *recipeVersionResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	var model recipeVersionModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &model)...)
	if response.Diagnostics.HasError() {
		return
	}
	if model.ReleaseType.IsNull() || model.ReleaseType.IsUnknown() {
		response.Diagnostics.AddAttributeError(path.Root("release_type"), "Missing recipe version release type", "Set release_type to MAJOR, MINOR, or PATCH when creating a recipe version. Imported versions can omit it because VEW does not return the original create instruction.")
		return
	}
	if model.ConfiguredComponents.IsNull() || model.ConfiguredComponents.IsUnknown() {
		response.Diagnostics.AddAttributeError(path.Root("configured_components"), "Missing recipe version components", "Set configured_components to an explicit list, or [] when creating a version without selected components.")
		return
	}
	timeout := recipeOperationTimeout(ctx, model.Timeouts, "create")
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	configured, diagnostics := recipeComponentConfig(ctx, request.Config)
	response.Diagnostics.Append(diagnostics...)
	if response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.Append(r.resolveRecipeComponentDetails(ctx, &model, configured)...)
	if response.Diagnostics.HasError() {
		return
	}
	mutable, diagnostics := recipeVersionInput(ctx, model)
	response.Diagnostics.Append(diagnostics...)
	if response.Diagnostics.HasError() {
		return
	}
	action, err := r.client.CreateRecipeVersion(ctx, model.ProjectID.ValueString(), model.RecipeID.ValueString(), vewrecipes.CreateRecipeVersionInput{
		Components: mutable.Components, Description: mutable.Description, ReleaseType: model.ReleaseType.ValueString(),
		VolumeSize: mutable.VolumeSize, Integrations: mutable.Integrations, BaseImageChannel: mutable.BaseImageChannel,
	})
	if err != nil {
		addRecipeVersionError(&response.Diagnostics, "create", err, "")
		return
	}
	model.ID = types.StringValue(action.ID)
	model.Status = types.StringValue("CREATING")
	model.Name, model.CreatedAt, model.CreatedBy, model.UpdatedAt, model.UpdatedBy = types.StringNull(), types.StringNull(), types.StringNull(), types.StringNull(), types.StringNull()
	model.EffectiveComponents = types.ListNull(types.ObjectType{AttrTypes: recipeComponentTypes})
	stateCtx := context.WithoutCancel(ctx)
	response.Diagnostics.Append(response.State.Set(stateCtx, &model)...)
	if response.Diagnostics.HasError() {
		return
	}
	err = r.waitVersion(ctx, &model, timeout, action.RetryAfter, validatedRecipeVersionStatus, false)
	response.Diagnostics.Append(response.State.Set(stateCtx, &model)...)
	if err != nil {
		addRecipeVersionError(&response.Diagnostics, "create", err, model.ID.ValueString())
	}
}

func (r *recipeVersionResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	var model recipeVersionModel
	response.Diagnostics.Append(request.State.Get(ctx, &model)...)
	if response.Diagnostics.HasError() {
		return
	}
	remote, err := r.client.GetRecipeVersion(ctx, model.ProjectID.ValueString(), model.RecipeID.ValueString(), model.ID.ValueString())
	if vew.IsNotFound(err) || (err == nil && remote.Status == "RETIRED") {
		response.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read VEW recipe version", recipeAPIDiagnostic("read", err))
		return
	}
	prior := model
	if err := setRecipeVersionState(ctx, &model, remote); err != nil {
		response.Diagnostics.AddError("Unable to read VEW recipe version", err.Error())
		return
	}
	if prior.ConfiguredComponents.IsNull() {
		// Import has no configured selection in state. Use the effective list as
		// the first-plan baseline; a differing declared selection then plans a
		// change without mutating VEW during import.
		model.ConfiguredComponents = model.EffectiveComponents
	}
	if request.Private != nil {
		retry, diagnostics := request.Private.GetKey(ctx, recipeVersionUpdateRetryKey)
		response.Diagnostics.Append(diagnostics...)
		if string(retry) == "true" {
			if remote.Status == "VALIDATED" {
				if response.Private != nil {
					response.Diagnostics.Append(response.Private.SetKey(ctx, recipeVersionUpdateRetryKey, nil)...)
				}
			} else {
				preserveRecipeVersionMutableConfiguration(&model, prior)
			}
		}
	}
	response.Diagnostics.Append(response.State.Set(ctx, &model)...)
}
