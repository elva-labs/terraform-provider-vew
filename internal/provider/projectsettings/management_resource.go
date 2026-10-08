// Package projectsettings implements the project-level settings resources:
// vew_project_management and vew_project_workbench_lifecycle.
package projectsettings

import (
	"context"
	"regexp"
	"strings"

	"github.com/elva-labs/terraform-provider-vew/internal/providerdata"
	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	api "github.com/elva-labs/terraform-provider-vew/internal/vew/projectsettings"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	schema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	managedByPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

	_ resource.Resource                   = (*managementResource)(nil)
	_ resource.ResourceWithConfigure      = (*managementResource)(nil)
	_ resource.ResourceWithImportState    = (*managementResource)(nil)
	_ resource.ResourceWithValidateConfig = (*managementResource)(nil)
)

type managementResource struct{ client api.API }

type managementModel struct {
	ID        types.String `tfsdk:"id"`
	ProjectID types.String `tfsdk:"project_id"`
	ManagedBy types.String `tfsdk:"managed_by"`
	Source    types.String `tfsdk:"source"`
}

// NewManagementResource constructs the vew_project_management resource.
func NewManagementResource() resource.Resource { return &managementResource{} }

func (r *managementResource) Metadata(_ context.Context, req resource.MetadataRequest, res *resource.MetadataResponse) {
	res.TypeName = req.ProviderTypeName + "_project_management"
}

func projectIDAttribute() schema.StringAttribute {
	return schema.StringAttribute{Required: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}}
}

func idAttribute() schema.StringAttribute {
	return schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}}
}

func (r *managementResource) Schema(_ context.Context, _ resource.SchemaRequest, res *resource.SchemaResponse) {
	res.Schema = schema.Schema{
		Description: "Marks a VEW project as managed by an external tool; VEW then refuses configuration changes made in the portal.",
		Attributes: map[string]schema.Attribute{
			"id":         idAttribute(),
			"project_id": projectIDAttribute(),
			"managed_by": schema.StringAttribute{
				Optional: true, Computed: true, Default: stringdefault.StaticString("terraform"),
				Description: "The managing tool, lowercase letters, digits and hyphens. Defaults to terraform.",
			},
			"source": schema.StringAttribute{
				Required:    true,
				Description: "Where the configuration lives, shown to users whose change is refused (for example a repository path).",
			},
		},
	}
}

func configure(req resource.ConfigureRequest, res *resource.ConfigureResponse) api.API {
	if req.ProviderData == nil {
		return nil
	}
	data, ok := req.ProviderData.(providerdata.Data)
	if !ok || strings.TrimSpace(data.ProjectAPIURL) == "" || data.ProjectSettings == nil {
		res.Diagnostics.AddError("Missing Projects API URL", "Project settings resources require a Projects API endpoint. Set projects_api_url or VEW_PROJECTS_API_URL.")
		return nil
	}
	return data.ProjectSettings
}

func (r *managementResource) Configure(_ context.Context, req resource.ConfigureRequest, res *resource.ConfigureResponse) {
	if client := configure(req, res); client != nil {
		r.client = client
	}
}

func (r *managementResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, res *resource.ValidateConfigResponse) {
	var m managementModel
	res.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if res.Diagnostics.HasError() {
		return
	}
	if known(m.ManagedBy) && !managedByPattern.MatchString(m.ManagedBy.ValueString()) {
		res.Diagnostics.AddAttributeError(path.Root("managed_by"), "Invalid managed_by", "Use lowercase letters, digits and hyphens (1-64 characters).")
	}
	if known(m.Source) && (strings.TrimSpace(m.Source.ValueString()) == "" || len(m.Source.ValueString()) > 256) {
		res.Diagnostics.AddAttributeError(path.Root("source"), "Invalid source", "source must be 1-256 characters.")
	}
}

func known(v types.String) bool { return !v.IsNull() && !v.IsUnknown() }

func (r *managementResource) put(ctx context.Context, m *managementModel, d *diag.Diagnostics) {
	err := r.client.PutManagement(ctx, m.ProjectID.ValueString(), api.ManagementInput{ManagedBy: m.ManagedBy.ValueString(), Source: m.Source.ValueString()})
	if err != nil {
		d.AddError("Setting VEW project management failed", err.Error())
		return
	}
	m.ID = m.ProjectID
}

func (r *managementResource) Create(ctx context.Context, req resource.CreateRequest, res *resource.CreateResponse) {
	var m managementModel
	res.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if res.Diagnostics.HasError() {
		return
	}
	r.put(ctx, &m, &res.Diagnostics)
	if !res.Diagnostics.HasError() {
		res.Diagnostics.Append(res.State.Set(ctx, &m)...)
	}
}

func (r *managementResource) Read(ctx context.Context, req resource.ReadRequest, res *resource.ReadResponse) {
	var m managementModel
	res.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if res.Diagnostics.HasError() {
		return
	}
	remote, err := r.client.GetManagement(ctx, m.ProjectID.ValueString())
	if vew.IsNotFound(err) {
		res.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		res.Diagnostics.AddError("Reading VEW project management failed", err.Error())
		return
	}
	m.ID, m.ManagedBy, m.Source = m.ProjectID, types.StringValue(remote.ManagedBy), types.StringValue(remote.Source)
	res.Diagnostics.Append(res.State.Set(ctx, &m)...)
}

func (r *managementResource) Update(ctx context.Context, req resource.UpdateRequest, res *resource.UpdateResponse) {
	var m managementModel
	res.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if res.Diagnostics.HasError() {
		return
	}
	r.put(ctx, &m, &res.Diagnostics)
	if !res.Diagnostics.HasError() {
		res.Diagnostics.Append(res.State.Set(ctx, &m)...)
	}
}

func (r *managementResource) Delete(ctx context.Context, req resource.DeleteRequest, res *resource.DeleteResponse) {
	var m managementModel
	res.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if res.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteManagement(ctx, m.ProjectID.ValueString()); err != nil {
		res.Diagnostics.AddError("Removing VEW project management failed", err.Error())
	}
}

func (r *managementResource) ImportState(ctx context.Context, req resource.ImportStateRequest, res *resource.ImportStateResponse) {
	importProjectID(ctx, req, res)
}

func importProjectID(ctx context.Context, req resource.ImportStateRequest, res *resource.ImportStateResponse) {
	id := strings.TrimSpace(req.ID)
	if id == "" || strings.Contains(id, "/") {
		res.Diagnostics.AddError("Invalid import ID", "Import with the VEW project ID.")
		return
	}
	res.Diagnostics.Append(res.State.SetAttribute(ctx, path.Root("project_id"), id)...)
	res.Diagnostics.Append(res.State.SetAttribute(ctx, path.Root("id"), id)...)
}
