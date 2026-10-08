// Package mandatorycomponents implements vew_mandatory_components_list.
package mandatorycomponents

import (
	"context"
	"strings"

	"github.com/elva-labs/terraform-provider-vew/internal/providerdata"
	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	api "github.com/elva-labs/terraform-provider-vew/internal/vew/mandatorycomponents"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	schema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = (*listResource)(nil)
	_ resource.ResourceWithConfigure   = (*listResource)(nil)
	_ resource.ResourceWithImportState = (*listResource)(nil)
)

type listResource struct{ client api.API }

type componentModel struct {
	ComponentID        types.String `tfsdk:"component_id"`
	ComponentVersionID types.String `tfsdk:"component_version_id"`
}

type listModel struct {
	ID             types.String     `tfsdk:"id"`
	ProjectID      types.String     `tfsdk:"project_id"`
	Platform       types.String     `tfsdk:"platform"`
	OSVersion      types.String     `tfsdk:"os_version"`
	Architecture   types.String     `tfsdk:"architecture"`
	Prepended      []componentModel `tfsdk:"prepended_components"`
	Appended       []componentModel `tfsdk:"appended_components"`
	LastUpdateDate types.String     `tfsdk:"last_update_date"`
}

// NewListResource constructs the vew_mandatory_components_list resource.
func NewListResource() resource.Resource { return &listResource{} }

func (r *listResource) Metadata(_ context.Context, req resource.MetadataRequest, res *resource.MetadataResponse) {
	res.TypeName = req.ProviderTypeName + "_mandatory_components_list"
}

func components(description string) schema.ListNestedAttribute {
	return schema.ListNestedAttribute{
		Optional:    true,
		Description: description,
		NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
			"component_id":         schema.StringAttribute{Required: true, Description: "The component."},
			"component_version_id": schema.StringAttribute{Required: true, Description: "A released version of the component."},
		}},
	}
}

func (r *listResource) Schema(_ context.Context, _ resource.SchemaRequest, res *resource.SchemaResponse) {
	replace := func(description string) schema.StringAttribute {
		return schema.StringAttribute{Required: true, Description: description, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}}
	}
	res.Schema = schema.Schema{
		Description: "Manages the mandatory components list of one platform, OS version and architecture: components VEW adds to every recipe version created on that OS.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"project_id": schema.StringAttribute{
				Required: true, Description: "The project the change is made for; when the deployment has a releasing project, it must be that one.",
			},
			"platform":             replace("The platform, for example Linux."),
			"os_version":           replace("The OS entry recipes name, for example \"Ubuntu 24\" or a base image channel's OS entry."),
			"architecture":         replace("The architecture, for example amd64."),
			"prepended_components": components("Component versions VEW runs before a recipe's own components, in this order."),
			"appended_components":  components("Component versions VEW runs after a recipe's own components, in this order."),
			"last_update_date":     schema.StringAttribute{Computed: true, Description: "When VEW last changed the list."},
		},
	}
}

func (r *listResource) Configure(_ context.Context, req resource.ConfigureRequest, res *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(providerdata.Data)
	if !ok || data.MandatoryComponents == nil {
		res.Diagnostics.AddError("Missing mandatory components client", "The provider could not configure the Packaging mandatory components list client.")
		return
	}
	r.client = data.MandatoryComponents
}

func (m *listModel) key() api.Key {
	return api.Key{Platform: m.Platform.ValueString(), OSVersion: m.OSVersion.ValueString(), Architecture: m.Architecture.ValueString()}
}

func refs(models []componentModel) []api.ComponentRef {
	out := make([]api.ComponentRef, 0, len(models))
	for _, m := range models {
		out = append(out, api.ComponentRef{ComponentID: m.ComponentID.ValueString(), ComponentVersionID: m.ComponentVersionID.ValueString()})
	}
	return out
}

// entries keeps an unset (null) list null when VEW reports it empty, so a
// configuration that omits it plans no change.
func entries(components []api.Component, prior []componentModel) []componentModel {
	if len(components) == 0 && prior == nil {
		return nil
	}
	out := make([]componentModel, 0, len(components))
	for _, c := range components {
		out = append(out, componentModel{ComponentID: types.StringValue(c.ComponentID), ComponentVersionID: types.StringValue(c.ComponentVersionID)})
	}
	return out
}

func (m *listModel) apply(l api.List) {
	m.ID = types.StringValue(l.Platform + "/" + l.OSVersion + "/" + l.Architecture)
	m.Platform, m.OSVersion, m.Architecture = types.StringValue(l.Platform), types.StringValue(l.OSVersion), types.StringValue(l.Architecture)
	m.Prepended = entries(l.Prepended, m.Prepended)
	m.Appended = entries(l.Appended, m.Appended)
	m.LastUpdateDate = types.StringPointerValue(l.LastUpdateDate)
}

func (r *listResource) put(ctx context.Context, m *listModel, d *diag.Diagnostics) {
	l, err := r.client.PutList(ctx, m.key(), api.PutInput{ProjectID: m.ProjectID.ValueString(), Prepended: refs(m.Prepended), Appended: refs(m.Appended)})
	if err != nil {
		d.AddError("Writing the VEW mandatory components list failed", err.Error())
		return
	}
	m.apply(l)
}

func (r *listResource) Create(ctx context.Context, req resource.CreateRequest, res *resource.CreateResponse) {
	var m listModel
	res.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if res.Diagnostics.HasError() {
		return
	}
	r.put(ctx, &m, &res.Diagnostics)
	if !res.Diagnostics.HasError() {
		res.Diagnostics.Append(res.State.Set(ctx, &m)...)
	}
}

func (r *listResource) Read(ctx context.Context, req resource.ReadRequest, res *resource.ReadResponse) {
	var m listModel
	res.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if res.Diagnostics.HasError() {
		return
	}
	l, err := r.client.GetList(ctx, m.key())
	if vew.IsNotFound(err) {
		res.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		res.Diagnostics.AddError("Reading the VEW mandatory components list failed", err.Error())
		return
	}
	m.apply(l)
	res.Diagnostics.Append(res.State.Set(ctx, &m)...)
}

func (r *listResource) Update(ctx context.Context, req resource.UpdateRequest, res *resource.UpdateResponse) {
	var m listModel
	res.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if res.Diagnostics.HasError() {
		return
	}
	r.put(ctx, &m, &res.Diagnostics)
	if !res.Diagnostics.HasError() {
		res.Diagnostics.Append(res.State.Set(ctx, &m)...)
	}
}

// Delete removes the list. Recipe versions created earlier keep the components
// they got; recipe versions created afterwards no longer receive them.
func (r *listResource) Delete(ctx context.Context, req resource.DeleteRequest, res *resource.DeleteResponse) {
	var m listModel
	res.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if res.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteList(ctx, m.key(), m.ProjectID.ValueString()); err != nil {
		res.Diagnostics.AddError("Deleting the VEW mandatory components list failed", err.Error())
	}
}

func (r *listResource) ImportState(ctx context.Context, req resource.ImportStateRequest, res *resource.ImportStateResponse) {
	parts := strings.Split(strings.TrimSpace(req.ID), "/")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		res.Diagnostics.AddError("Invalid import ID", "Import with platform/os_version/architecture, for example Linux/Ubuntu 24/amd64.")
		return
	}
	res.Diagnostics.Append(res.State.SetAttribute(ctx, path.Root("platform"), parts[0])...)
	res.Diagnostics.Append(res.State.SetAttribute(ctx, path.Root("os_version"), parts[1])...)
	res.Diagnostics.Append(res.State.SetAttribute(ctx, path.Root("architecture"), parts[2])...)
	res.Diagnostics.Append(res.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
