package recipes

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/elva-labs/terraform-provider-vew/internal/providerdata"
	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	vewrecipes "github.com/elva-labs/terraform-provider-vew/internal/vew/recipes"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	schema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                   = (*recipeResource)(nil)
	_ resource.ResourceWithConfigure      = (*recipeResource)(nil)
	_ resource.ResourceWithImportState    = (*recipeResource)(nil)
	_ resource.ResourceWithValidateConfig = (*recipeResource)(nil)
)

type recipeResource struct{ client vewrecipes.RecipeAPI }

type recipeModel struct {
	ID           types.String `tfsdk:"id"`
	ProjectID    types.String `tfsdk:"project_id"`
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

func NewRecipeResource() resource.Resource { return &recipeResource{} }

func (r *recipeResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_recipe"
}

func recipeReplacementString() schema.StringAttribute {
	return schema.StringAttribute{Required: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}}
}

func (r *recipeResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{Attributes: map[string]schema.Attribute{
		"id":           schema.StringAttribute{Computed: true},
		"project_id":   recipeReplacementString(),
		"name":         recipeReplacementString(),
		"description":  recipeReplacementString(),
		"platform":     recipeReplacementString(),
		"architecture": recipeReplacementString(),
		"os_version":   recipeReplacementString(),
		"status":       schema.StringAttribute{Computed: true},
		"created_at":   schema.StringAttribute{Computed: true},
		"created_by":   schema.StringAttribute{Computed: true},
		"updated_at":   schema.StringAttribute{Computed: true},
		"updated_by":   schema.StringAttribute{Computed: true},
	}}
}

func (r *recipeResource) Configure(_ context.Context, request resource.ConfigureRequest, response *resource.ConfigureResponse) {
	if request.ProviderData == nil {
		return
	}
	data, ok := request.ProviderData.(providerdata.Data)
	if !ok || data.Recipes == nil {
		response.Diagnostics.AddError("Missing Recipe API", "Expected provider data to include a Recipes API.")
		return
	}
	r.client = data.Recipes
}

func (r *recipeResource) ValidateConfig(ctx context.Context, request resource.ValidateConfigRequest, response *resource.ValidateConfigResponse) {
	var config recipeModel
	response.Diagnostics.Append(request.Config.Get(ctx, &config)...)
	if response.Diagnostics.HasError() {
		return
	}
	for name, value := range map[string]types.String{"project_id": config.ProjectID, "name": config.Name, "description": config.Description, "platform": config.Platform, "architecture": config.Architecture, "os_version": config.OSVersion} {
		if !value.IsUnknown() && !value.IsNull() && strings.TrimSpace(value.ValueString()) == "" {
			response.Diagnostics.AddAttributeError(path.Root(name), "Empty recipe value", name+" must be non-empty.")
		}
	}
	if config.Platform.IsUnknown() || config.Architecture.IsUnknown() || config.Platform.IsNull() || config.Architecture.IsNull() {
		return
	}
	platform, architecture := config.Platform.ValueString(), config.Architecture.ValueString()
	if !validRecipeSystem(platform, architecture) {
		response.Diagnostics.AddAttributeError(path.Root("platform"), "Unsupported recipe system configuration", "Use Linux with amd64 or arm64, or Windows with amd64.")
	}
}

// validRecipeSystem checks only the platform and architecture pair. The OS version is not checked
// here: a VEW deployment defines its OS entries in its system configuration mapping (for example a
// custom base image next to the built-in Ubuntu and Windows entries), and the VEW API rejects
// entries the deployment does not offer.
func validRecipeSystem(platform, architecture string) bool {
	return (platform == "Linux" && (architecture == "amd64" || architecture == "arm64")) ||
		(platform == "Windows" && architecture == "amd64")
}

func (r *recipeResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	projectID, recipeID, err := parseRecipeImportID(request.ID)
	if err != nil {
		response.Diagnostics.AddError("Invalid recipe import ID", "Expected project_id/recipe_id.")
		return
	}
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("project_id"), projectID)...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), recipeID)...)
}

func parseRecipeImportID(value string) (string, string, error) {
	parts := strings.Split(value, "/")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", errors.New("expected project_id/recipe_id")
	}
	return parts[0], parts[1], nil
}

func (r *recipeResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	var plan recipeModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	id, err := r.client.CreateRecipe(ctx, plan.ProjectID.ValueString(), vewrecipes.CreateRecipeInput{Name: plan.Name.ValueString(), Description: plan.Description.ValueString(), Platform: plan.Platform.ValueString(), Architecture: plan.Architecture.ValueString(), OSVersion: plan.OSVersion.ValueString()})
	if err != nil {
		response.Diagnostics.AddError("Unable to create VEW recipe", recipeAPIDiagnostic("create", err))
		return
	}
	remote, err := r.client.GetRecipe(ctx, plan.ProjectID.ValueString(), id)
	if err != nil {
		plan.ID = types.StringValue(id)
		plan.Status, plan.CreatedAt, plan.CreatedBy, plan.UpdatedAt, plan.UpdatedBy = types.StringNull(), types.StringNull(), types.StringNull(), types.StringNull(), types.StringNull()
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
		if !response.Diagnostics.HasError() {
			response.Diagnostics.AddWarning("VEW recipe created with provisional state", fmt.Sprintf("Recipe %q was created, but canonical state could not be read. Terraform will refresh it later.", id))
		}
		return
	}
	setRecipeState(&plan, remote)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func (r *recipeResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	var state recipeModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	r.read(ctx, &state, &response.State, &response.Diagnostics)
}

func (r *recipeResource) read(ctx context.Context, state *recipeModel, terraformState *tfsdk.State, diagnostics *diag.Diagnostics) {
	remote, err := r.client.GetRecipe(ctx, state.ProjectID.ValueString(), state.ID.ValueString())
	if vew.IsNotFound(err) {
		terraformState.RemoveResource(ctx)
		return
	}
	if err != nil {
		diagnostics.AddError("Unable to read VEW recipe", recipeAPIDiagnostic("read", err))
		return
	}
	if remote.Status == "ARCHIVED" {
		terraformState.RemoveResource(ctx)
		return
	}
	setRecipeState(state, remote)
	diagnostics.Append(terraformState.Set(ctx, state)...)
}

func (r *recipeResource) Update(_ context.Context, _ resource.UpdateRequest, response *resource.UpdateResponse) {
	response.Diagnostics.AddError("Recipe update requires replacement", "All recipe configuration fields are create-only in VEW.")
}

func (r *recipeResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	var state recipeModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	if err := r.client.ArchiveRecipe(ctx, state.ProjectID.ValueString(), state.ID.ValueString()); err != nil {
		summary, detail := recipeArchiveDiagnostic(state.ID.ValueString(), err)
		response.Diagnostics.AddError(summary, detail)
	}
}

func setRecipeState(state *recipeModel, remote vewrecipes.Recipe) {
	state.ID = types.StringValue(remote.ID)
	state.Name = types.StringValue(remote.Name)
	state.Description = types.StringValue(remote.Description)
	state.Platform = types.StringValue(remote.Platform)
	state.Architecture = types.StringValue(remote.Architecture)
	state.OSVersion = types.StringValue(remote.OSVersion)
	state.Status = types.StringValue(remote.Status)
	state.CreatedAt = types.StringValue(remote.CreatedAt)
	state.CreatedBy = types.StringValue(remote.CreatedBy)
	state.UpdatedAt = types.StringValue(remote.UpdatedAt)
	state.UpdatedBy = types.StringValue(remote.UpdatedBy)
}

func recipeAPIDiagnostic(operation string, err error) string {
	message := "VEW API " + operation + " failed"
	var apiError *vew.APIError
	if !errors.As(err, &apiError) {
		return message
	}
	parts := []string{fmt.Sprintf("HTTP status %d", apiError.Status)}
	if code := apiError.Problem.Code; safeRecipeCorrelation.MatchString(code) {
		parts = append(parts, "problem code "+code)
	}
	if requestID := apiError.Problem.RequestID; safeRecipeCorrelation.MatchString(requestID) {
		parts = append(parts, "request ID "+requestID)
	}
	return message + " (" + strings.Join(parts, ", ") + ")"
}
