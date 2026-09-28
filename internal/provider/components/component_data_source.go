package components

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/elva-labs/terraform-provider-vew/internal/providerdata"
	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	vewcomponents "github.com/elva-labs/terraform-provider-vew/internal/vew/components"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &componentDataSource{}
var _ datasource.DataSourceWithConfigure = &componentDataSource{}
var _ datasource.DataSourceWithValidateConfig = &componentDataSource{}

type componentDataSource struct {
	client vewcomponents.ComponentReadAPI
}

type componentDataSourceModel struct {
	ProjectID              types.String `tfsdk:"project_id"`
	ComponentID            types.String `tfsdk:"component_id"`
	Name                   types.String `tfsdk:"name"`
	Description            types.String `tfsdk:"description"`
	Platform               types.String `tfsdk:"platform"`
	SupportedArchitectures types.Set    `tfsdk:"supported_architectures"`
	SupportedOSVersions    types.Set    `tfsdk:"supported_os_versions"`
	Status                 types.String `tfsdk:"status"`
	CreatedAt              types.String `tfsdk:"created_at"`
	CreatedBy              types.String `tfsdk:"created_by"`
	UpdatedAt              types.String `tfsdk:"updated_at"`
	UpdatedBy              types.String `tfsdk:"updated_by"`
}

var safeComponentIdentifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)

// NewComponentDataSource constructs the exact component reader.
func NewComponentDataSource() datasource.DataSource { return &componentDataSource{} }

func (d *componentDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_component"
}
func (d *componentDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{Attributes: map[string]schema.Attribute{
		"project_id": schema.StringAttribute{Required: true}, "component_id": schema.StringAttribute{Required: true},
		"name": schema.StringAttribute{Computed: true}, "description": schema.StringAttribute{Computed: true}, "platform": schema.StringAttribute{Computed: true},
		"supported_architectures": schema.SetAttribute{Computed: true, ElementType: types.StringType}, "supported_os_versions": schema.SetAttribute{Computed: true, ElementType: types.StringType},
		"status": schema.StringAttribute{Computed: true}, "created_at": schema.StringAttribute{Computed: true}, "created_by": schema.StringAttribute{Computed: true}, "updated_at": schema.StringAttribute{Computed: true}, "updated_by": schema.StringAttribute{Computed: true},
	}}
}
func (d *componentDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(providerdata.Data)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Data Source Configure Type", "Expected provider data to be providerdata.Data.")
		return
	}
	if data.ComponentReads == nil {
		resp.Diagnostics.AddError("Missing Component Read API", "Expected provider data to include a read-only ComponentReads API.")
		return
	}
	d.client = data.ComponentReads
}
func (d *componentDataSource) ValidateConfig(ctx context.Context, req datasource.ValidateConfigRequest, resp *datasource.ValidateConfigResponse) {
	var m componentDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	validateSelector(&resp.Diagnostics, "project_id", m.ProjectID, true)
	validateSelector(&resp.Diagnostics, "component_id", m.ComponentID, true)
}
func (d *componentDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m componentDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	validateSelector(&resp.Diagnostics, "project_id", m.ProjectID, false)
	validateSelector(&resp.Diagnostics, "component_id", m.ComponentID, false)
	if resp.Diagnostics.HasError() {
		return
	}
	if d.client == nil {
		resp.Diagnostics.AddError("Missing Component Read API", "The component data source has no configured read-only client.")
		return
	}
	c, err := d.client.GetComponent(ctx, m.ProjectID.ValueString(), m.ComponentID.ValueString())
	if vew.IsNotFound(err) {
		resp.Diagnostics.AddError("VEW component not found", "The requested project-scoped component was not found.")
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read VEW component", safeComponentReadDiagnostic(err))
		return
	}
	if !componentResponseMatchesSelector(c, m.ComponentID.ValueString()) {
		resp.Diagnostics.AddError("Invalid VEW component response", "VEW returned a component whose identifier did not match the requested component.")
		return
	}
	m.Name = types.StringValue(c.Name)
	m.Description = types.StringValue(c.Description)
	m.Platform = types.StringValue(c.Platform)
	architectures, ds := types.SetValueFrom(ctx, types.StringType, c.SupportedArchitectures)
	resp.Diagnostics.Append(ds...)
	if ds.HasError() {
		return
	}
	m.SupportedArchitectures = architectures
	osVersions, ds := types.SetValueFrom(ctx, types.StringType, c.SupportedOSVersions)
	resp.Diagnostics.Append(ds...)
	if ds.HasError() {
		return
	}
	m.SupportedOSVersions = osVersions
	m.Status = types.StringValue(c.Status)
	m.CreatedAt = types.StringValue(c.CreatedAt)
	m.CreatedBy = types.StringValue(c.CreatedBy)
	m.UpdatedAt = types.StringValue(c.UpdatedAt)
	m.UpdatedBy = types.StringValue(c.UpdatedBy)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func componentResponseMatchesSelector(component vewcomponents.Component, selector string) bool {
	return safeComponentIdentifier.MatchString(component.ID) && component.ID == selector
}

func validateSelector(d *diag.Diagnostics, name string, v types.String, allowUnknown bool) {
	if v.IsUnknown() && allowUnknown {
		return
	}
	if v.IsNull() || v.IsUnknown() || !safeComponentIdentifier.MatchString(v.ValueString()) {
		d.AddAttributeError(path.Root(name), "Invalid data source selector", name+" must match ^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$ with no surrounding whitespace.")
	}
}
func safeComponentReadDiagnostic(err error) string {
	message := "VEW component read failed"
	var apiErr *vew.APIError
	if !errors.As(err, &apiErr) {
		return message
	}
	parts := []string{fmt.Sprintf("HTTP status %d", apiErr.Status)}
	if safeComponentIdentifier.MatchString(apiErr.Problem.Code) {
		parts = append(parts, "problem code "+apiErr.Problem.Code)
	}
	if safeComponentIdentifier.MatchString(apiErr.Problem.RequestID) {
		parts = append(parts, "request ID "+apiErr.Problem.RequestID)
	}
	return message + " (" + strings.Join(parts, ", ") + ")"
}
