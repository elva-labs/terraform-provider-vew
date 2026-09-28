package recipes

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/elva-labs/terraform-provider-vew/internal/providerdata"
	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	vewrecipes "github.com/elva-labs/terraform-provider-vew/internal/vew/recipes"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource                   = (*recipeDataSource)(nil)
	_ datasource.DataSourceWithConfigure      = (*recipeDataSource)(nil)
	_ datasource.DataSourceWithValidateConfig = (*recipeDataSource)(nil)
	_ datasource.DataSource                   = (*recipeVersionDataSource)(nil)
	_ datasource.DataSourceWithConfigure      = (*recipeVersionDataSource)(nil)
	_ datasource.DataSourceWithValidateConfig = (*recipeVersionDataSource)(nil)
)

type recipeDataSource struct{ client vewrecipes.RecipeReadAPI }
type recipeDataSourceModel struct {
	ProjectID    types.String `tfsdk:"project_id"`
	RecipeID     types.String `tfsdk:"recipe_id"`
	Name         types.String `tfsdk:"name"`
	Description  types.String `tfsdk:"description"`
	Platform     types.String `tfsdk:"platform"`
	Architecture types.String `tfsdk:"architecture"`
	OSVersion    types.String `tfsdk:"os_version"`
	Status       types.String `tfsdk:"status"`
	CreatedAt    types.String `tfsdk:"created_at"`
	CreatedBy    types.String `tfsdk:"created_by"`
	UpdatedAt    types.String `tfsdk:"updated_at"`
	UpdatedBy    types.String `tfsdk:"updated_by"`
}

// NewRecipeDataSource creates vew_recipe.
func NewRecipeDataSource() datasource.DataSource { return &recipeDataSource{} }

func (d *recipeDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_recipe"
}

func (d *recipeDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{Attributes: map[string]schema.Attribute{
		"project_id": schema.StringAttribute{Required: true}, "recipe_id": schema.StringAttribute{Required: true},
		"name": schema.StringAttribute{Computed: true}, "description": schema.StringAttribute{Computed: true},
		"platform": schema.StringAttribute{Computed: true}, "architecture": schema.StringAttribute{Computed: true},
		"os_version": schema.StringAttribute{Computed: true}, "status": schema.StringAttribute{Computed: true},
		"created_at": schema.StringAttribute{Computed: true}, "created_by": schema.StringAttribute{Computed: true},
		"updated_at": schema.StringAttribute{Computed: true}, "updated_by": schema.StringAttribute{Computed: true},
	}}
}

func (d *recipeDataSource) Configure(_ context.Context, request datasource.ConfigureRequest, response *datasource.ConfigureResponse) {
	if request.ProviderData == nil {
		return
	}
	data, ok := request.ProviderData.(providerdata.Data)
	if !ok || data.RecipeReads == nil {
		response.Diagnostics.AddError("Missing Recipe Read API", "Expected provider data to include a read-only RecipeReads API.")
		return
	}
	d.client = data.RecipeReads
}

func (d *recipeDataSource) ValidateConfig(ctx context.Context, request datasource.ValidateConfigRequest, response *datasource.ValidateConfigResponse) {
	var model recipeDataSourceModel
	response.Diagnostics.Append(request.Config.Get(ctx, &model)...)
	validateRecipeSelectors(&response.Diagnostics,
		true, recipeSelector{"project_id", model.ProjectID}, recipeSelector{"recipe_id", model.RecipeID})
}

func (d *recipeDataSource) Read(ctx context.Context, request datasource.ReadRequest, response *datasource.ReadResponse) {
	var model recipeDataSourceModel
	response.Diagnostics.Append(request.Config.Get(ctx, &model)...)
	if response.Diagnostics.HasError() {
		return
	}
	validateRecipeSelectors(&response.Diagnostics,
		false, recipeSelector{"project_id", model.ProjectID}, recipeSelector{"recipe_id", model.RecipeID})
	if response.Diagnostics.HasError() {
		return
	}
	if d.client == nil {
		response.Diagnostics.AddError("Missing Recipe Read API", "The recipe data source has no configured read-only client.")
		return
	}
	remote, err := d.client.GetRecipe(ctx, model.ProjectID.ValueString(), model.RecipeID.ValueString())
	if err != nil {
		if vew.IsNotFound(err) {
			response.Diagnostics.AddError("VEW recipe not found", fmt.Sprintf("No recipe %s was found in project %s.", safeRecipeTarget(model.RecipeID.ValueString()), safeRecipeTarget(model.ProjectID.ValueString())))
		} else {
			response.Diagnostics.AddError("Unable to read VEW recipe", safeRecipeReadDiagnostic("read recipe", err, model.RecipeID.ValueString()))
		}
		return
	}
	if err := validateRecipeRemoteIdentity(model.RecipeID.ValueString(), remote); err != nil {
		response.Diagnostics.AddError("Unable to read VEW recipe", "VEW returned a recipe ID that does not match the requested recipe_id.")
		return
	}
	model.Name, model.Description = types.StringValue(remote.Name), types.StringValue(remote.Description)
	model.Platform, model.Architecture, model.OSVersion = types.StringValue(remote.Platform), types.StringValue(remote.Architecture), types.StringValue(remote.OSVersion)
	model.Status = types.StringValue(remote.Status)
	model.CreatedAt, model.CreatedBy = types.StringValue(remote.CreatedAt), types.StringValue(remote.CreatedBy)
	model.UpdatedAt, model.UpdatedBy = types.StringValue(remote.UpdatedAt), types.StringValue(remote.UpdatedBy)
	response.Diagnostics.Append(response.State.Set(ctx, &model)...)
}

type recipeVersionDataSource struct {
	client vewrecipes.RecipeVersionReadAPI
}
type recipeVersionDataSourceModel struct {
	ProjectID            types.String `tfsdk:"project_id"`
	RecipeID             types.String `tfsdk:"recipe_id"`
	VersionID            types.String `tfsdk:"version_id"`
	Description          types.String `tfsdk:"description"`
	VolumeSize           types.Int64  `tfsdk:"volume_size"`
	Integrations         types.Set    `tfsdk:"integrations"`
	ConfiguredComponents types.List   `tfsdk:"configured_components"`
	EffectiveComponents  types.List   `tfsdk:"effective_components"`
	Name                 types.String `tfsdk:"name"`
	Status               types.String `tfsdk:"status"`
	CreatedAt            types.String `tfsdk:"created_at"`
	CreatedBy            types.String `tfsdk:"created_by"`
	UpdatedAt            types.String `tfsdk:"updated_at"`
	UpdatedBy            types.String `tfsdk:"updated_by"`
}

// NewRecipeVersionDataSource creates vew_recipe_version.
func NewRecipeVersionDataSource() datasource.DataSource { return &recipeVersionDataSource{} }

func (d *recipeVersionDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_recipe_version"
}

func (d *recipeVersionDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	computedComponents := func() schema.ListNestedAttribute {
		return schema.ListNestedAttribute{Computed: true, NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
			"component_id": schema.StringAttribute{Computed: true}, "component_name": schema.StringAttribute{Computed: true},
			"version_id": schema.StringAttribute{Computed: true}, "version_name": schema.StringAttribute{Computed: true},
			"type": schema.StringAttribute{Computed: true}, "order": schema.Int64Attribute{Computed: true},
		}}}
	}
	response.Schema = schema.Schema{Attributes: map[string]schema.Attribute{
		"project_id": schema.StringAttribute{Required: true},
		"recipe_id":  schema.StringAttribute{Required: true}, "version_id": schema.StringAttribute{Required: true},
		"description": schema.StringAttribute{Computed: true}, "volume_size": schema.Int64Attribute{Computed: true},
		"integrations":          schema.SetAttribute{Computed: true, ElementType: types.StringType},
		"configured_components": computedComponents(), "effective_components": computedComponents(),
		"name": schema.StringAttribute{Computed: true}, "status": schema.StringAttribute{Computed: true},
		"created_at": schema.StringAttribute{Computed: true}, "created_by": schema.StringAttribute{Computed: true},
		"updated_at": schema.StringAttribute{Computed: true}, "updated_by": schema.StringAttribute{Computed: true},
	}}
}

func (d *recipeVersionDataSource) Configure(_ context.Context, request datasource.ConfigureRequest, response *datasource.ConfigureResponse) {
	if request.ProviderData == nil {
		return
	}
	data, ok := request.ProviderData.(providerdata.Data)
	if !ok || data.RecipeVersionReads == nil {
		response.Diagnostics.AddError("Missing Recipe Version Read API", "Expected provider data to include a read-only RecipeVersionReads API.")
		return
	}
	d.client = data.RecipeVersionReads
}

func (d *recipeVersionDataSource) ValidateConfig(ctx context.Context, request datasource.ValidateConfigRequest, response *datasource.ValidateConfigResponse) {
	var model recipeVersionDataSourceModel
	response.Diagnostics.Append(request.Config.Get(ctx, &model)...)
	validateRecipeSelectors(&response.Diagnostics,
		true, recipeSelector{"project_id", model.ProjectID}, recipeSelector{"recipe_id", model.RecipeID}, recipeSelector{"version_id", model.VersionID})
}

func (d *recipeVersionDataSource) Read(ctx context.Context, request datasource.ReadRequest, response *datasource.ReadResponse) {
	var model recipeVersionDataSourceModel
	response.Diagnostics.Append(request.Config.Get(ctx, &model)...)
	if response.Diagnostics.HasError() {
		return
	}
	validateRecipeSelectors(&response.Diagnostics,
		false, recipeSelector{"project_id", model.ProjectID}, recipeSelector{"recipe_id", model.RecipeID}, recipeSelector{"version_id", model.VersionID})
	if response.Diagnostics.HasError() {
		return
	}
	if d.client == nil {
		response.Diagnostics.AddError("Missing Recipe Version Read API", "The recipe version data source has no configured read-only client.")
		return
	}
	remote, err := d.client.GetRecipeVersion(ctx, model.ProjectID.ValueString(), model.RecipeID.ValueString(), model.VersionID.ValueString())
	if err != nil {
		if vew.IsNotFound(err) {
			response.Diagnostics.AddError("VEW recipe version not found", fmt.Sprintf("No version %s for recipe %s was found in project %s.", safeRecipeTarget(model.VersionID.ValueString()), safeRecipeTarget(model.RecipeID.ValueString()), safeRecipeTarget(model.ProjectID.ValueString())))
		} else {
			response.Diagnostics.AddError("Unable to read VEW recipe version", safeRecipeReadDiagnostic("read recipe version", err, model.VersionID.ValueString()))
		}
		return
	}
	if err := validateRecipeVersionRemoteIdentity(model.RecipeID.ValueString(), model.VersionID.ValueString(), remote); err != nil {
		response.Diagnostics.AddError("Unable to read VEW recipe version", "VEW returned a recipe version or parent recipe ID that does not match the requested selectors.")
		return
	}
	model.Description = types.StringValue(remote.Description)
	model.Name, model.Status = types.StringValue(remote.Name), types.StringValue(remote.Status)
	model.CreatedAt, model.CreatedBy = types.StringValue(remote.CreatedAt), types.StringValue(remote.CreatedBy)
	model.UpdatedAt, model.UpdatedBy = types.StringValue(remote.UpdatedAt), types.StringValue(remote.UpdatedBy)
	volume, parseErr := strconv.ParseInt(strings.TrimSpace(remote.VolumeSize), 10, 64)
	if parseErr != nil || volume < 0 {
		response.Diagnostics.AddError("Unable to read VEW recipe version", "VEW returned an invalid recipe version volume size.")
		return
	}
	model.VolumeSize = types.Int64Value(volume)
	integrations := remote.Integrations
	if integrations == nil {
		integrations = []string{}
	}
	model.Integrations, response.Diagnostics = types.SetValueFrom(ctx, types.StringType, integrations)
	if response.Diagnostics.HasError() {
		response.Diagnostics.AddError("Unable to read VEW recipe version", "VEW returned invalid recipe version integrations.")
		return
	}
	if remote.Components == nil {
		model.ConfiguredComponents = types.ListNull(types.ObjectType{AttrTypes: recipeComponentTypes})
	} else {
		var diagnostics diag.Diagnostics
		model.ConfiguredComponents, diagnostics = recipeComponentList(ctx, *remote.Components)
		response.Diagnostics.Append(diagnostics...)
	}
	if response.Diagnostics.HasError() {
		return
	}
	var diagnostics diag.Diagnostics
	model.EffectiveComponents, diagnostics = recipeComponentList(ctx, remote.EffectiveComponents)
	response.Diagnostics.Append(diagnostics...)
	if response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &model)...)
}

func validateRecipeRemoteIdentity(requestedID string, remote vewrecipes.Recipe) error {
	if remote.ID != requestedID {
		return fmt.Errorf("recipe response ID mismatch")
	}
	return nil
}

func validateRecipeVersionRemoteIdentity(requestedRecipeID, requestedVersionID string, remote vewrecipes.RecipeVersion) error {
	if remote.ID != requestedVersionID || remote.RecipeID != requestedRecipeID {
		return fmt.Errorf("recipe version response identity mismatch")
	}
	return nil
}

var validRecipeSelector = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)

type recipeSelector struct {
	name  string
	value types.String
}

func validateRecipeSelectors(diagnostics *diag.Diagnostics, allowUnknown bool, selectors ...recipeSelector) {
	for _, selector := range selectors {
		name, value := selector.name, selector.value
		if value.IsUnknown() && allowUnknown {
			continue
		}
		if value.IsUnknown() || value.IsNull() || strings.TrimSpace(value.ValueString()) == "" {
			diagnostics.AddAttributeError(path.Root(name), "Invalid recipe selector", name+" must be a known, non-empty VEW identifier.")
			continue
		}
		if value.ValueString() != strings.TrimSpace(value.ValueString()) {
			diagnostics.AddAttributeError(path.Root(name), "Invalid recipe selector", name+" must not contain surrounding whitespace.")
			continue
		}
		if !validRecipeSelector.MatchString(value.ValueString()) {
			diagnostics.AddAttributeError(path.Root(name), "Invalid recipe selector", name+" must contain only letters, numbers, dots, underscores, colons, or hyphens and start with a letter or number.")
		}
	}
}

func safeRecipeTarget(value string) string {
	if validRecipeSelector.MatchString(value) {
		return fmt.Sprintf("%q", value)
	}
	return "<invalid>"
}

func safeRecipeReadDiagnostic(operation string, err error, targetID string) string {
	message := recipeAPIDiagnostic(operation, err)
	if validRecipeSelector.MatchString(targetID) {
		return message + fmt.Sprintf(" Target ID %q.", targetID)
	}
	return message
}
