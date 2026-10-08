// Package baseimages implements vew_base_image_release.
package baseimages

import (
	"context"
	"strings"

	"github.com/elva-labs/terraform-provider-vew/internal/providerdata"
	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	api "github.com/elva-labs/terraform-provider-vew/internal/vew/baseimages"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	schema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = (*releaseResource)(nil)
	_ resource.ResourceWithConfigure   = (*releaseResource)(nil)
	_ resource.ResourceWithImportState = (*releaseResource)(nil)
)

type releaseResource struct{ client api.API }

type releaseModel struct {
	ID            types.String `tfsdk:"id"`
	ProjectID     types.String `tfsdk:"project_id"`
	Architecture  types.String `tfsdk:"architecture"`
	Channel       types.String `tfsdk:"channel"`
	ImageID       types.String `tfsdk:"image_id"`
	OSVersion     types.String `tfsdk:"os_version"`
	ParameterName types.String `tfsdk:"parameter_name"`
	AmiID         types.String `tfsdk:"ami_id"`
	PreviousAmiID types.String `tfsdk:"previous_ami_id"`
}

// NewReleaseResource constructs the vew_base_image_release resource.
func NewReleaseResource() resource.Resource { return &releaseResource{} }

func (r *releaseResource) Metadata(_ context.Context, req resource.MetadataRequest, res *resource.MetadataResponse) {
	res.TypeName = req.ProviderTypeName + "_base_image_release"
}

func (r *releaseResource) Schema(_ context.Context, _ resource.SchemaRequest, res *resource.SchemaResponse) {
	replace := func(description string) schema.StringAttribute {
		return schema.StringAttribute{Required: true, Description: description, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}}
	}
	computed := func(description string) schema.StringAttribute {
		return schema.StringAttribute{Computed: true, Description: description}
	}
	res.Schema = schema.Schema{
		Description: "Releases an image to one of the deployment's base-image channels, which every project's recipes build on.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"project_id": schema.StringAttribute{
				Required: true, Description: "The project allowed to release base images; the image must be one of its builds.",
			},
			"architecture":    replace("The image architecture (for example amd64)."),
			"channel":         replace("The channel (for example test or prod). VEW may require an image to pass another channel first."),
			"image_id":        schema.StringAttribute{Required: true, Description: "The VEW image to release. Changing it releases the new image in place; the replaced AMI is kept as previous_ami_id."},
			"os_version":      computed("The OS entry recipes select for this channel."),
			"parameter_name":  computed("The parameter VEW resolves the channel from."),
			"ami_id":          computed("The released AMI."),
			"previous_ami_id": computed("The AMI the last release replaced (rollback)."),
		},
	}
}

func (r *releaseResource) Configure(_ context.Context, req resource.ConfigureRequest, res *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(providerdata.Data)
	if !ok || data.BaseImages == nil {
		res.Diagnostics.AddError("Missing base image client", "The provider could not configure the Packaging base image client.")
		return
	}
	r.client = data.BaseImages
}

func str(v *string) types.String { return types.StringPointerValue(v) }

func (m *releaseModel) apply(b api.BaseImage) {
	m.ID = types.StringValue(b.Architecture + "/" + b.Channel)
	m.Architecture, m.Channel = types.StringValue(b.Architecture), types.StringValue(b.Channel)
	if b.ProjectID != nil {
		m.ProjectID = types.StringValue(*b.ProjectID)
	}
	m.ImageID = str(b.ImageID)
	m.OSVersion, m.ParameterName = types.StringValue(b.OSVersion), types.StringValue(b.ParameterName)
	m.AmiID, m.PreviousAmiID = str(b.AmiID), str(b.PreviousAmiID)
}

func (r *releaseResource) release(ctx context.Context, m *releaseModel, d *diag.Diagnostics) {
	b, err := r.client.ReleaseBaseImage(ctx, m.Architecture.ValueString(), m.Channel.ValueString(),
		api.ReleaseInput{ProjectID: m.ProjectID.ValueString(), ImageID: m.ImageID.ValueString()})
	if err != nil {
		d.AddError("Releasing the VEW base image failed", err.Error())
		return
	}
	m.apply(b)
}

func (r *releaseResource) Create(ctx context.Context, req resource.CreateRequest, res *resource.CreateResponse) {
	var m releaseModel
	res.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if res.Diagnostics.HasError() {
		return
	}
	r.release(ctx, &m, &res.Diagnostics)
	if !res.Diagnostics.HasError() {
		res.Diagnostics.Append(res.State.Set(ctx, &m)...)
	}
}

func (r *releaseResource) Read(ctx context.Context, req resource.ReadRequest, res *resource.ReadResponse) {
	var m releaseModel
	res.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if res.Diagnostics.HasError() {
		return
	}
	b, err := r.client.GetBaseImage(ctx, m.Architecture.ValueString(), m.Channel.ValueString())
	if vew.IsNotFound(err) {
		res.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		res.Diagnostics.AddError("Reading the VEW base image failed", err.Error())
		return
	}
	m.apply(b)
	res.Diagnostics.Append(res.State.Set(ctx, &m)...)
}

func (r *releaseResource) Update(ctx context.Context, req resource.UpdateRequest, res *resource.UpdateResponse) {
	var m releaseModel
	res.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if res.Diagnostics.HasError() {
		return
	}
	r.release(ctx, &m, &res.Diagnostics)
	if !res.Diagnostics.HasError() {
		res.Diagnostics.Append(res.State.Set(ctx, &m)...)
	}
}

// Delete only forgets the release: a base image is never withdrawn, only
// replaced, because every project's next recipe version resolves the channel.
func (r *releaseResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {}

func (r *releaseResource) ImportState(ctx context.Context, req resource.ImportStateRequest, res *resource.ImportStateResponse) {
	parts := strings.Split(strings.TrimSpace(req.ID), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		res.Diagnostics.AddError("Invalid import ID", "Import with architecture/channel, for example amd64/prod.")
		return
	}
	res.Diagnostics.Append(res.State.SetAttribute(ctx, path.Root("architecture"), parts[0])...)
	res.Diagnostics.Append(res.State.SetAttribute(ctx, path.Root("channel"), parts[1])...)
	res.Diagnostics.Append(res.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
