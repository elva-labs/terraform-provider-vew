package images

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/elva-labs/terraform-provider-vew/internal/providerdata"
	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	vewimages "github.com/elva-labs/terraform-provider-vew/internal/vew/images"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = (*imageDataSource)(nil)
var _ datasource.DataSourceWithConfigure = (*imageDataSource)(nil)
var _ datasource.DataSourceWithValidateConfig = (*imageDataSource)(nil)
var _ datasource.DataSource = (*imagesDataSource)(nil)
var _ datasource.DataSourceWithConfigure = (*imagesDataSource)(nil)
var _ datasource.DataSourceWithValidateConfig = (*imagesDataSource)(nil)

type imageDataSource struct{ api vewimages.ReadAPI }
type imagesDataSource struct{ api vewimages.ReadAPI }

func NewImageDataSource() datasource.DataSource  { return &imageDataSource{} }
func NewImagesDataSource() datasource.DataSource { return &imagesDataSource{} }

func (d *imageDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_image"
}
func (d *imagesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_images"
}
func (d *imageDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{Attributes: map[string]schema.Attribute{
		"project_id":        schema.StringAttribute{Required: true, Description: "Project containing the image."},
		"image_id":          schema.StringAttribute{Required: true, Description: "Exact image ID to read."},
		"pipeline_id":       schema.StringAttribute{Computed: true},
		"status":            schema.StringAttribute{Computed: true},
		"image_upstream_id": schema.StringAttribute{Computed: true, Description: "Nullable upstream image ID."},
	}}
}
func (d *imagesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{Attributes: map[string]schema.Attribute{
		"project_id":  schema.StringAttribute{Required: true, Description: "Project whose images are listed."},
		"pipeline_id": schema.StringAttribute{Optional: true, Description: "Optional nonempty pipeline ID filter."},
		"status":      schema.StringAttribute{Optional: true, Description: "Optional lifecycle status filter: CREATING, CREATED, FAILED, RETIRED, or DELETED."},
		"images": schema.ListNestedAttribute{Computed: true, NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
			"project_id":        schema.StringAttribute{Computed: true},
			"image_id":          schema.StringAttribute{Computed: true},
			"pipeline_id":       schema.StringAttribute{Computed: true},
			"status":            schema.StringAttribute{Computed: true},
			"image_upstream_id": schema.StringAttribute{Computed: true, Description: "Nullable upstream image ID."},
		}}},
	}}
}

func (d *imageDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(providerdata.Data)
	if !ok || data.ImageReads == nil {
		resp.Diagnostics.AddError("Missing Image Read API", "Expected provider data to include a read-only image API.")
		return
	}
	d.api = data.ImageReads
}
func (d *imagesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(providerdata.Data)
	if !ok || data.ImageReads == nil {
		resp.Diagnostics.AddError("Missing Image Read API", "Expected provider data to include a read-only image API.")
		return
	}
	d.api = data.ImageReads
}

type exactModel struct {
	ProjectID  types.String `tfsdk:"project_id"`
	ImageID    types.String `tfsdk:"image_id"`
	PipelineID types.String `tfsdk:"pipeline_id"`
	Status     types.String `tfsdk:"status"`
	UpstreamID types.String `tfsdk:"image_upstream_id"`
}
type collectionModel struct {
	ProjectID  types.String `tfsdk:"project_id"`
	PipelineID types.String `tfsdk:"pipeline_id"`
	Status     types.String `tfsdk:"status"`
	Images     types.List   `tfsdk:"images"`
}

func (d *imageDataSource) ValidateConfig(ctx context.Context, req datasource.ValidateConfigRequest, resp *datasource.ValidateConfigResponse) {
	var m exactModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	validateRequired(&resp.Diagnostics, "project_id", m.ProjectID, true)
	validateRequired(&resp.Diagnostics, "image_id", m.ImageID, true)
}
func (d *imagesDataSource) ValidateConfig(ctx context.Context, req datasource.ValidateConfigRequest, resp *datasource.ValidateConfigResponse) {
	var m collectionModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	validateRequired(&resp.Diagnostics, "project_id", m.ProjectID, true)
	validateCollectionFilters(&resp.Diagnostics, m.PipelineID, m.Status, false)
}

func (d *imageDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m exactModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !validateRequired(&resp.Diagnostics, "project_id", m.ProjectID, false) || !validateRequired(&resp.Diagnostics, "image_id", m.ImageID, false) {
		return
	}
	if d.api == nil {
		resp.Diagnostics.AddError("Unable to read VEW image", "The read-only image client is not configured.")
		return
	}
	remote, err := d.api.GetImage(ctx, m.ProjectID.ValueString(), m.ImageID.ValueString())
	if err != nil {
		addReadError(&resp.Diagnostics, "read", m.ImageID.ValueString(), err)
		return
	}
	if err := validateImageIdentity(remote, m.ProjectID.ValueString(), m.ImageID.ValueString()); err != nil {
		if strings.Contains(err.Error(), "project") {
			resp.Diagnostics.AddError("Invalid VEW image response", "VEW returned an image from a different project than requested.")
		} else {
			resp.Diagnostics.AddError("Invalid VEW image response", "VEW returned an image without the requested image ID.")
		}
		return
	}
	m.PipelineID, m.Status, m.UpstreamID = types.StringValue(remote.PipelineID), types.StringValue(remote.Status), nullable(remote.UpstreamID)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (d *imagesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m collectionModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !validateRequired(&resp.Diagnostics, "project_id", m.ProjectID, false) {
		return
	}
	if !validateCollectionFilters(&resp.Diagnostics, m.PipelineID, m.Status, true) {
		return
	}
	if d.api == nil {
		resp.Diagnostics.AddError("Unable to list VEW images", "The read-only image client is not configured.")
		return
	}
	remote, err := d.api.ListImages(ctx, m.ProjectID.ValueString())
	if err != nil {
		addReadError(&resp.Diagnostics, "list", "", err)
		return
	}
	for _, image := range remote {
		if image.ProjectID != "" && image.ProjectID != m.ProjectID.ValueString() {
			resp.Diagnostics.AddError("Invalid VEW image response", "VEW returned an image from a different project than requested.")
			return
		}
	}
	filtered, filterErr := filterAndSortImages(remote, m.PipelineID, m.Status)
	if filterErr != nil {
		resp.Diagnostics.AddError("Invalid VEW image response", "VEW returned an image without an image ID.")
		return
	}
	list, diags := imageListValue(ctx, filtered, m.ProjectID.ValueString())
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	m.Images = list
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func validateCollectionFilters(d *diag.Diagnostics, pipelineID, status types.String, requireKnown bool) bool {
	valid := true
	if pipelineID.IsUnknown() {
		if requireKnown {
			d.AddAttributeError(path.Root("pipeline_id"), "Invalid image filter", "pipeline_id must be known when reading images.")
			valid = false
		}
	} else if !pipelineID.IsNull() {
		value := pipelineID.ValueString()
		if strings.TrimSpace(value) == "" {
			d.AddAttributeError(path.Root("pipeline_id"), "Invalid image filter", "pipeline_id must be nonempty when provided.")
			valid = false
		} else if strings.TrimSpace(value) != value || !safeID.MatchString(value) {
			d.AddAttributeError(path.Root("pipeline_id"), "Invalid image filter", "pipeline_id must be a valid identifier without surrounding whitespace.")
			valid = false
		}
	}
	if status.IsUnknown() {
		if requireKnown {
			d.AddAttributeError(path.Root("status"), "Invalid image status", "status must be known when reading images.")
			valid = false
		}
	} else if !status.IsNull() && !knownStatus(status.ValueString()) {
		d.AddAttributeError(path.Root("status"), "Invalid image status", "status must be one of CREATING, CREATED, FAILED, RETIRED, or DELETED.")
		valid = false
	}
	return valid
}

func validateImageIdentity(image vewimages.Image, projectID, imageID string) error {
	if strings.TrimSpace(image.ID) == "" || image.ID != imageID {
		return errors.New("image ID mismatch")
	}
	if image.ProjectID != "" && image.ProjectID != projectID {
		return errors.New("image project ID mismatch")
	}
	return nil
}

type imageListItem struct {
	ProjectID  types.String `tfsdk:"project_id"`
	ID         types.String `tfsdk:"image_id"`
	PipelineID types.String `tfsdk:"pipeline_id"`
	Status     types.String `tfsdk:"status"`
	UpstreamID types.String `tfsdk:"image_upstream_id"`
}

func imageListValue(ctx context.Context, images []vewimages.Image, projectID string) (types.List, diag.Diagnostics) {
	items := make([]imageListItem, len(images))
	for i, image := range images {
		itemProjectID := image.ProjectID
		if itemProjectID == "" {
			itemProjectID = projectID
		}
		items[i] = imageListItem{types.StringValue(itemProjectID), types.StringValue(image.ID), types.StringValue(image.PipelineID), types.StringValue(image.Status), nullable(image.UpstreamID)}
	}
	return types.ListValueFrom(ctx, types.ObjectType{AttrTypes: map[string]attr.Type{"project_id": types.StringType, "image_id": types.StringType, "pipeline_id": types.StringType, "status": types.StringType, "image_upstream_id": types.StringType}}, items)
}

func filterAndSortImages(remote []vewimages.Image, pipelineID, status types.String) ([]vewimages.Image, error) {
	if pipelineID.IsUnknown() || status.IsUnknown() {
		return nil, errors.New("image filters must be known")
	}
	filtered := make([]vewimages.Image, 0, len(remote))
	for _, image := range remote {
		if strings.TrimSpace(image.ID) == "" {
			return nil, errors.New("image missing ID")
		}
		if !pipelineID.IsNull() && image.PipelineID != pipelineID.ValueString() {
			continue
		}
		if !status.IsNull() && image.Status != status.ValueString() {
			continue
		}
		filtered = append(filtered, image)
	}
	sort.Slice(filtered, func(i, j int) bool { return filtered[i].ID < filtered[j].ID })
	return filtered, nil
}

func validateRequired(d *diag.Diagnostics, name string, value types.String, allowUnknown bool) bool {
	if value.IsUnknown() && allowUnknown {
		return true
	}
	if value.IsNull() || value.IsUnknown() || strings.TrimSpace(value.ValueString()) == "" {
		d.AddAttributeError(path.Root(name), "Invalid image selector", name+" must be known and nonempty before reading VEW.")
		return false
	}
	return true
}
func nullable(value *string) types.String {
	if value == nil {
		return types.StringNull()
	}
	return types.StringValue(*value)
}
func knownStatus(status string) bool {
	switch status {
	case "CREATING", "CREATED", "FAILED", "RETIRED", "DELETED":
		return true
	}
	return false
}

var safeID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)

func addReadError(d *diag.Diagnostics, operation, id string, err error) {
	detail := "VEW image " + operation + " failed. Refresh and retry."
	var api *vew.APIError
	if vew.IsNotFound(err) {
		detail = "The requested project-scoped VEW image was not found."
	} else if errors.As(err, &api) {
		detail = fmt.Sprintf("VEW image %s failed (HTTP status %d).", operation, api.Status)
		if safeID.MatchString(api.Problem.Code) {
			detail += " VEW code: " + api.Problem.Code + "."
		}
		if safeID.MatchString(api.Problem.RequestID) {
			detail += " Request ID: " + api.Problem.RequestID + "."
		}
	}
	if safeID.MatchString(id) {
		detail += " Image ID: " + id + "."
	}
	d.AddError("Unable to "+operation+" VEW image", detail)
}
