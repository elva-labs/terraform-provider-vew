package components

import (
	"context"
	"sort"
	"strings"

	"github.com/elva-labs/terraform-provider-vew/internal/providerdata"
	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	vewcomponents "github.com/elva-labs/terraform-provider-vew/internal/vew/components"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &componentVersionDataSource{}
var _ datasource.DataSourceWithConfigure = &componentVersionDataSource{}
var _ datasource.DataSourceWithValidateConfig = &componentVersionDataSource{}

type componentVersionDataSource struct {
	client vewcomponents.ComponentVersionReadAPI
}

type componentVersionDataSourceModel struct {
	ProjectID        types.String `tfsdk:"project_id"`
	ComponentID      types.String `tfsdk:"component_id"`
	VersionID        types.String `tfsdk:"version_id"`
	Description      types.String `tfsdk:"description"`
	DefinitionJSON   types.String `tfsdk:"definition_json"`
	Dependencies     types.List   `tfsdk:"dependencies"`
	SoftwareVendor   types.String `tfsdk:"software_vendor"`
	SoftwareVersion  types.String `tfsdk:"software_version"`
	LicenseDashboard types.String `tfsdk:"license_dashboard"`
	Notes            types.String `tfsdk:"notes"`
	Name             types.String `tfsdk:"name"`
	Status           types.String `tfsdk:"status"`
	CreatedAt        types.String `tfsdk:"created_at"`
	CreatedBy        types.String `tfsdk:"created_by"`
	UpdatedAt        types.String `tfsdk:"updated_at"`
	UpdatedBy        types.String `tfsdk:"updated_by"`
}

func NewComponentVersionDataSource() datasource.DataSource { return &componentVersionDataSource{} }
func (d *componentVersionDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_component_version"
}
func (d *componentVersionDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	depAttrs := map[string]schema.Attribute{"component_id": schema.StringAttribute{Computed: true}, "component_name": schema.StringAttribute{Computed: true}, "version_id": schema.StringAttribute{Computed: true}, "version_name": schema.StringAttribute{Computed: true}, "type": schema.StringAttribute{Computed: true}, "order": schema.Int64Attribute{Computed: true}, "position": schema.StringAttribute{Computed: true}}
	resp.Schema = schema.Schema{Attributes: map[string]schema.Attribute{
		"project_id": schema.StringAttribute{Required: true}, "component_id": schema.StringAttribute{Required: true}, "version_id": schema.StringAttribute{Required: true},
		"description": schema.StringAttribute{Computed: true}, "definition_json": schema.StringAttribute{Computed: true}, "dependencies": schema.ListNestedAttribute{Computed: true, NestedObject: schema.NestedAttributeObject{Attributes: depAttrs}},
		"software_vendor": schema.StringAttribute{Computed: true}, "software_version": schema.StringAttribute{Computed: true}, "license_dashboard": schema.StringAttribute{Computed: true}, "notes": schema.StringAttribute{Computed: true},
		"name": schema.StringAttribute{Computed: true}, "status": schema.StringAttribute{Computed: true}, "created_at": schema.StringAttribute{Computed: true}, "created_by": schema.StringAttribute{Computed: true}, "updated_at": schema.StringAttribute{Computed: true}, "updated_by": schema.StringAttribute{Computed: true},
	}}
}
func (d *componentVersionDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(providerdata.Data)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Data Source Configure Type", "Expected provider data to be providerdata.Data.")
		return
	}
	if data.ComponentVersionReads == nil {
		resp.Diagnostics.AddError("Missing Component Version Read API", "Expected provider data to include a read-only ComponentVersionReads API.")
		return
	}
	d.client = data.ComponentVersionReads
}
func (d *componentVersionDataSource) ValidateConfig(ctx context.Context, req datasource.ValidateConfigRequest, resp *datasource.ValidateConfigResponse) {
	var m componentVersionDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	validateSelector(&resp.Diagnostics, "project_id", m.ProjectID, true)
	validateSelector(&resp.Diagnostics, "component_id", m.ComponentID, true)
	validateSelector(&resp.Diagnostics, "version_id", m.VersionID, true)
}
func (d *componentVersionDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m componentVersionDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	validateSelector(&resp.Diagnostics, "project_id", m.ProjectID, false)
	validateSelector(&resp.Diagnostics, "component_id", m.ComponentID, false)
	validateSelector(&resp.Diagnostics, "version_id", m.VersionID, false)
	if resp.Diagnostics.HasError() {
		return
	}
	if d.client == nil {
		resp.Diagnostics.AddError("Missing Component Version Read API", "The component version data source has no configured read-only client.")
		return
	}
	v, err := d.client.GetComponentVersion(ctx, m.ProjectID.ValueString(), m.ComponentID.ValueString(), m.VersionID.ValueString())
	if vew.IsNotFound(err) {
		resp.Diagnostics.AddError("VEW component version not found", "The requested project-scoped component version was not found.")
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read VEW component version", safeComponentReadDiagnostic(err))
		return
	}
	if !componentVersionResponseMatchesSelectors(v, m.ComponentID.ValueString(), m.VersionID.ValueString()) {
		resp.Diagnostics.AddError("Invalid VEW component version response", "VEW returned a component version whose identifiers did not match the requested component and version.")
		return
	}
	m.ComponentID = types.StringValue(v.ComponentID)
	m.VersionID = types.StringValue(v.ID)
	m.Description = types.StringValue(v.Description)
	m.Name = types.StringValue(v.Name)
	m.SoftwareVendor = types.StringValue(v.SoftwareVendor)
	m.SoftwareVersion = types.StringValue(v.SoftwareVersion)
	m.LicenseDashboard = types.StringPointerValue(v.LicenseDashboard)
	m.Notes = types.StringPointerValue(v.Notes)
	m.Status = types.StringValue(v.Status)
	m.CreatedAt = types.StringValue(v.CreatedAt)
	m.CreatedBy = types.StringValue(v.CreatedBy)
	m.UpdatedAt = types.StringValue(v.UpdatedAt)
	m.UpdatedBy = types.StringValue(v.UpdatedBy)
	definition := strings.TrimSpace(string(v.Definition))
	if definition == "" || definition == "null" {
		m.DefinitionJSON = types.StringNull()
	} else {
		canonical, e := normalizeDefinition(definition)
		if e != nil {
			resp.Diagnostics.AddError("Invalid VEW component version definition", "The VEW component version definition could not be represented as a canonical JSON object.")
			return
		}
		m.DefinitionJSON = types.StringValue(string(canonical))
	}
	deps := make([]dependencyModel, 0, len(v.Dependencies))
	for _, dep := range v.Dependencies {
		deps = append(deps, dependencyModel{ComponentID: types.StringValue(dep.ComponentID), ComponentName: types.StringValue(dep.ComponentName), VersionID: types.StringValue(dep.VersionID), VersionName: types.StringValue(dep.VersionName), Type: types.StringValue(dep.Type), Order: types.Int64Value(dep.Order), Position: types.StringPointerValue(dep.Position)})
	}
	sort.SliceStable(deps, func(i, j int) bool { return deps[i].Order.ValueInt64() < deps[j].Order.ValueInt64() })
	value, diags := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: componentVersionDependencyAttributeTypes}, deps)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	m.Dependencies = value
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func componentVersionResponseMatchesSelectors(version vewcomponents.ComponentVersion, componentSelector, versionSelector string) bool {
	return safeComponentIdentifier.MatchString(version.ID) && version.ID == versionSelector &&
		safeComponentIdentifier.MatchString(version.ComponentID) && version.ComponentID == componentSelector
}
