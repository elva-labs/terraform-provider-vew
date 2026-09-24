package releaseactions

import (
	"context"
	"regexp"
	"strings"

	"github.com/elva-labs/terraform-provider-vew/internal/providerdata"
	"github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/action/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// ComponentVersionReleaseAPI is deliberately narrower than the resource API.
type ComponentVersionReleaseAPI interface {
	ReleaseComponentVersion(context.Context, string, string, string) error
}

// RecipeVersionReleaseAPI is deliberately narrower than the resource API.
type RecipeVersionReleaseAPI interface {
	ReleaseRecipeVersion(context.Context, string, string, string) error
}

type releaseAction struct {
	kind      string
	component ComponentVersionReleaseAPI
	recipe    RecipeVersionReleaseAPI
}

type releaseModel struct {
	ProjectID   types.String `tfsdk:"project_id"`
	ComponentID types.String `tfsdk:"component_id"`
	RecipeID    types.String `tfsdk:"recipe_id"`
	VersionID   types.String `tfsdk:"version_id"`
}

var _ action.ActionWithConfigure = (*releaseAction)(nil)

func NewComponentVersionReleaseAction() action.Action {
	return &releaseAction{kind: "component"}
}

func NewRecipeVersionReleaseAction() action.Action {
	return &releaseAction{kind: "recipe"}
}

func (a *releaseAction) Metadata(_ context.Context, request action.MetadataRequest, response *action.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_" + a.kind + "_version_release"
}

func (a *releaseAction) Schema(_ context.Context, _ action.SchemaRequest, response *action.SchemaResponse) {
	parentName := a.kind + "_id"
	response.Schema = schema.Schema{
		Description: "Releases an existing validated VEW " + a.kind + " version. Release is one-way and must be explicitly invoked.",
		Attributes: map[string]schema.Attribute{
			"project_id": schema.StringAttribute{Required: true, Description: "Project containing the version."},
			parentName:   schema.StringAttribute{Required: true, Description: "Parent " + a.kind + " ID."},
			"version_id": schema.StringAttribute{Required: true, Description: "Existing version to release."},
		},
	}
}

func (a *releaseAction) Configure(_ context.Context, request action.ConfigureRequest, response *action.ConfigureResponse) {
	if request.ProviderData == nil {
		return
	}
	data, ok := request.ProviderData.(providerdata.Data)
	if !ok {
		response.Diagnostics.AddError("Unable to configure VEW release action", "Unexpected provider data type.")
		return
	}
	a.component = data.ComponentVersionReleases
	a.recipe = data.RecipeVersionReleases
}

func (a *releaseAction) Invoke(ctx context.Context, request action.InvokeRequest, response *action.InvokeResponse) {
	var model releaseModel
	// A schema omits the other parent's ID. Decode only the declared attributes.
	var target struct {
		ProjectID types.String `tfsdk:"project_id"`
		ParentID  types.String `tfsdk:"component_id"`
		VersionID types.String `tfsdk:"version_id"`
	}
	if a.kind == "component" {
		response.Diagnostics.Append(request.Config.Get(ctx, &target)...)
		model.ProjectID, model.ComponentID, model.VersionID = target.ProjectID, target.ParentID, target.VersionID
	} else {
		var recipeTarget struct {
			ProjectID types.String `tfsdk:"project_id"`
			ParentID  types.String `tfsdk:"recipe_id"`
			VersionID types.String `tfsdk:"version_id"`
		}
		response.Diagnostics.Append(request.Config.Get(ctx, &recipeTarget)...)
		model.ProjectID, model.RecipeID, model.VersionID = recipeTarget.ProjectID, recipeTarget.ParentID, recipeTarget.VersionID
	}
	if response.Diagnostics.HasError() {
		return
	}
	parentName, parentID := a.kind+"_id", model.ComponentID
	if a.kind == "recipe" {
		parentID = model.RecipeID
	}
	valid := requiredID(&response.Diagnostics, "project_id", model.ProjectID)
	valid = requiredID(&response.Diagnostics, parentName, parentID) && valid
	valid = requiredID(&response.Diagnostics, "version_id", model.VersionID) && valid
	if !valid {
		return
	}
	if a.kind == "component" {
		if a.component == nil {
			response.Diagnostics.AddError("Unable to release VEW component version", "The release client is not configured.")
			return
		}
		if err := a.component.ReleaseComponentVersion(ctx, model.ProjectID.ValueString(), parentID.ValueString(), model.VersionID.ValueString()); err != nil {
			addReleaseError(&response.Diagnostics, a.kind, model.ProjectID.ValueString(), parentID.ValueString(), model.VersionID.ValueString(), err)
		}
		return
	}
	if a.recipe == nil {
		response.Diagnostics.AddError("Unable to release VEW recipe version", "The release client is not configured.")
		return
	}
	if err := a.recipe.ReleaseRecipeVersion(ctx, model.ProjectID.ValueString(), parentID.ValueString(), model.VersionID.ValueString()); err != nil {
		addReleaseError(&response.Diagnostics, a.kind, model.ProjectID.ValueString(), parentID.ValueString(), model.VersionID.ValueString(), err)
	}
}

func requiredID(diagnostics *diag.Diagnostics, name string, value types.String) bool {
	if value.IsUnknown() || value.IsNull() || strings.TrimSpace(value.ValueString()) == "" {
		diagnostics.AddError("Invalid release action ID", name+" must be known and nonempty when the release action is invoked.")
		return false
	}
	return true
}

var safeIdentifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)
