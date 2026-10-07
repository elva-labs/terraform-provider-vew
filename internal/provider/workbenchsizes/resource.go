// Package workbenchsizes implements vew_program_member_sizes: the workbench sizes and disks a user may
// use in a VEW project beyond everyone's, on the Provisioning S2S API.
package workbenchsizes

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/elva-labs/terraform-provider-vew/internal/providerdata"
	api "github.com/elva-labs/terraform-provider-vew/internal/vew/workbenchsizes"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	schema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = (*sizesResource)(nil)
	_ resource.ResourceWithConfigure   = (*sizesResource)(nil)
	_ resource.ResourceWithImportState = (*sizesResource)(nil)
)

type sizesResource struct{ client api.API }

type sizesModel struct {
	ID        types.String `tfsdk:"id"`
	ProjectID types.String `tfsdk:"project_id"`
	UserID    types.String `tfsdk:"user_id"`
	UserEmail types.String `tfsdk:"user_email"`
	Sizes     types.Set    `tfsdk:"sizes"`
	UpdatedBy types.String `tfsdk:"updated_by"`
	UpdatedAt types.String `tfsdk:"updated_at"`
}

// NewResource constructs the vew_program_member_sizes resource.
func NewResource() resource.Resource { return &sizesResource{} }

func (r *sizesResource) Metadata(_ context.Context, req resource.MetadataRequest, res *resource.MetadataResponse) {
	res.TypeName = req.ProviderTypeName + "_program_member_sizes"
}

func (r *sizesResource) Schema(_ context.Context, _ resource.SchemaRequest, res *resource.SchemaResponse) {
	immutable := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	computed := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	res.Schema = schema.Schema{
		Description: "The workbench sizes and disks a user may use in a VEW project beyond everyone's " +
			"(the deployment's size catalog). Destroying it returns the user to everyone's sizes.",
		Attributes: map[string]schema.Attribute{
			"id":         schema.StringAttribute{Computed: true, PlanModifiers: computed, Description: "project_id/user_id."},
			"project_id": schema.StringAttribute{Required: true, PlanModifiers: immutable},
			"user_id":    schema.StringAttribute{Required: true, PlanModifiers: immutable, Description: "The VEW user ID."},
			"user_email": schema.StringAttribute{
				Optional: true, Computed: true, PlanModifiers: computed,
				Description: "Shown next to the grant in the portal; kept once set.",
			},
			"sizes": schema.SetAttribute{
				Required: true, ElementType: types.StringType,
				Description: "Catalog keys beyond everyone's, for example standard-m, standard-l, gpu-s, gpu-m, gpu-l, " +
					"disk-500, disk-1000. An empty set keeps the record with everyone's sizes.",
			},
			"updated_by": schema.StringAttribute{Computed: true, PlanModifiers: computed},
			"updated_at": schema.StringAttribute{Computed: true, PlanModifiers: computed},
		},
	}
}

func (r *sizesResource) Configure(_ context.Context, req resource.ConfigureRequest, res *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(providerdata.Data)
	if !ok || data.WorkbenchSizes == nil {
		res.Diagnostics.AddError("Missing Provisioning API URL",
			"vew_program_member_sizes requires a Provisioning API endpoint. Set provisioning_api_url or VEW_PROVISIONING_API_URL.")
		return
	}
	r.client = data.WorkbenchSizes
}

func sizesOf(ctx context.Context, set types.Set, d *diag.Diagnostics) []string {
	var sizes []string
	d.Append(set.ElementsAs(ctx, &sizes, false)...)
	sort.Strings(sizes)
	return sizes
}

func (r *sizesResource) apply(ctx context.Context, m *sizesModel, d *diag.Diagnostics) {
	sizes := sizesOf(ctx, m.Sizes, d)
	if d.HasError() {
		return
	}
	email := ""
	if !m.UserEmail.IsNull() && !m.UserEmail.IsUnknown() {
		email = m.UserEmail.ValueString()
	}
	grant, err := r.client.Put(ctx, m.ProjectID.ValueString(), m.UserID.ValueString(), sizes, email)
	if err != nil {
		d.AddError("Setting VEW workbench sizes failed", err.Error())
		return
	}
	r.fill(m, grant, d)
}

func (r *sizesResource) fill(m *sizesModel, g api.Grant, d *diag.Diagnostics) {
	m.ID = types.StringValue(m.ProjectID.ValueString() + "/" + m.UserID.ValueString())
	// The configured e-mail wins in state (VEW keeps the first one it was given); otherwise VEW's.
	if m.UserEmail.IsNull() || m.UserEmail.IsUnknown() {
		m.UserEmail = types.StringNull()
		if g.UserEmail != "" {
			m.UserEmail = types.StringValue(g.UserEmail)
		}
	}
	values := make([]attr.Value, 0, len(g.Sizes))
	for _, s := range g.Sizes {
		values = append(values, types.StringValue(s))
	}
	set, diags := types.SetValue(types.StringType, values)
	d.Append(diags...)
	m.Sizes = set
	m.UpdatedBy, m.UpdatedAt = types.StringValue(g.UpdatedBy), types.StringValue(g.UpdatedAt)
}

func (r *sizesResource) Create(ctx context.Context, req resource.CreateRequest, res *resource.CreateResponse) {
	var m sizesModel
	res.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if res.Diagnostics.HasError() {
		return
	}
	r.apply(ctx, &m, &res.Diagnostics)
	if !res.Diagnostics.HasError() {
		res.Diagnostics.Append(res.State.Set(ctx, &m)...)
	}
}

func (r *sizesResource) Read(ctx context.Context, req resource.ReadRequest, res *resource.ReadResponse) {
	var m sizesModel
	res.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if res.Diagnostics.HasError() {
		return
	}
	grant, err := r.client.Get(ctx, m.ProjectID.ValueString(), m.UserID.ValueString())
	if errors.Is(err, api.ErrNotGranted) {
		res.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		res.Diagnostics.AddError("Reading VEW workbench sizes failed", err.Error())
		return
	}
	r.fill(&m, grant, &res.Diagnostics)
	res.Diagnostics.Append(res.State.Set(ctx, &m)...)
}

func (r *sizesResource) Update(ctx context.Context, req resource.UpdateRequest, res *resource.UpdateResponse) {
	var m sizesModel
	res.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if res.Diagnostics.HasError() {
		return
	}
	r.apply(ctx, &m, &res.Diagnostics)
	if !res.Diagnostics.HasError() {
		res.Diagnostics.Append(res.State.Set(ctx, &m)...)
	}
}

func (r *sizesResource) Delete(ctx context.Context, req resource.DeleteRequest, res *resource.DeleteResponse) {
	var m sizesModel
	res.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if res.Diagnostics.HasError() {
		return
	}
	if err := r.client.Delete(ctx, m.ProjectID.ValueString(), m.UserID.ValueString()); err != nil {
		res.Diagnostics.AddError("Removing VEW workbench sizes failed", err.Error())
	}
}

func (r *sizesResource) ImportState(ctx context.Context, req resource.ImportStateRequest, res *resource.ImportStateResponse) {
	parts := strings.SplitN(strings.TrimSpace(req.ID), "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		res.Diagnostics.AddError("Invalid import ID", "Import with project_id/user_id.")
		return
	}
	res.Diagnostics.Append(res.State.SetAttribute(ctx, path.Root("project_id"), parts[0])...)
	res.Diagnostics.Append(res.State.SetAttribute(ctx, path.Root("user_id"), parts[1])...)
	res.Diagnostics.Append(res.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
