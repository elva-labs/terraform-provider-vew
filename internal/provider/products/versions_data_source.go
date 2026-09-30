package products

import (
	"context"
	"strings"

	"github.com/elva-labs/terraform-provider-vew/internal/providerdata"
	vewproducts "github.com/elva-labs/terraform-provider-vew/internal/vew/products"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource                   = (*versionsDataSource)(nil)
	_ datasource.DataSourceWithConfigure      = (*versionsDataSource)(nil)
	_ datasource.DataSourceWithValidateConfig = (*versionsDataSource)(nil)
)

type versionsDataSource struct {
	client vewproducts.VersionReadAPI
}

type versionsDataSourceModel struct {
	ProjectID types.String `tfsdk:"project_id"`
	ProductID types.String `tfsdk:"product_id"`
	Versions  types.List   `tfsdk:"versions"`
}

var (
	versionStageAttributeTypes = map[string]attr.Type{"stage": types.StringType, "status": types.StringType}
	versionAttributeTypes      = map[string]attr.Type{
		"version_id": types.StringType, "version_name": types.StringType, "version_type": types.StringType,
		"stages": types.ListType{ElemType: types.ObjectType{AttrTypes: versionStageAttributeTypes}},
	}
)

// NewProductVersionsDataSource constructs the vew_product_versions data source.
func NewProductVersionsDataSource() datasource.DataSource { return &versionsDataSource{} }

func (d *versionsDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_product_versions"
}

func (d *versionsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		Description: "Lists the versions of a VEW product with the stages each is released to.",
		Attributes: map[string]schema.Attribute{
			"project_id": schema.StringAttribute{Required: true},
			"product_id": schema.StringAttribute{Required: true},
			"versions": schema.ListNestedAttribute{Computed: true, NestedObject: schema.NestedAttributeObject{
				Attributes: map[string]schema.Attribute{
					"version_id":   schema.StringAttribute{Computed: true},
					"version_name": schema.StringAttribute{Computed: true},
					"version_type": schema.StringAttribute{Computed: true},
					"stages": schema.ListNestedAttribute{Computed: true, NestedObject: schema.NestedAttributeObject{
						Attributes: map[string]schema.Attribute{
							"stage":  schema.StringAttribute{Computed: true},
							"status": schema.StringAttribute{Computed: true},
						},
					}},
				},
			}},
		},
	}
}

func (d *versionsDataSource) Configure(_ context.Context, request datasource.ConfigureRequest, response *datasource.ConfigureResponse) {
	if request.ProviderData == nil {
		return
	}
	data, ok := request.ProviderData.(providerdata.Data)
	if !ok || strings.TrimSpace(data.PublishingAPIURL) == "" || data.ProductVersionReads == nil {
		response.Diagnostics.AddError("Missing Publishing API URL", "The vew_product_versions data source requires a Publishing API endpoint. Set publishing_api_url or VEW_PUBLISHING_API_URL.")
		return
	}
	d.client = data.ProductVersionReads
}

func (d *versionsDataSource) ValidateConfig(ctx context.Context, request datasource.ValidateConfigRequest, response *datasource.ValidateConfigResponse) {
	var config versionsDataSourceModel
	response.Diagnostics.Append(request.Config.Get(ctx, &config)...)
	if response.Diagnostics.HasError() {
		return
	}
	for name, value := range map[string]types.String{"project_id": config.ProjectID, "product_id": config.ProductID} {
		if known(value) && strings.TrimSpace(value.ValueString()) == "" {
			response.Diagnostics.AddAttributeError(path.Root(name), "Empty product versions value", name+" must be non-empty.")
		}
	}
}

func (d *versionsDataSource) Read(ctx context.Context, request datasource.ReadRequest, response *datasource.ReadResponse) {
	var model versionsDataSourceModel
	response.Diagnostics.Append(request.Config.Get(ctx, &model)...)
	if response.Diagnostics.HasError() {
		return
	}
	versions, err := d.client.ListProductVersions(ctx, model.ProjectID.ValueString(), model.ProductID.ValueString())
	if err != nil {
		addPromotionError(&response.Diagnostics, "read", err, model.ProjectID.ValueString())
		return
	}
	list, diagnostics := versionsValue(versions)
	response.Diagnostics.Append(diagnostics...)
	if response.Diagnostics.HasError() {
		return
	}
	model.Versions = list
	response.Diagnostics.Append(response.State.Set(ctx, &model)...)
}

func versionsValue(versions []vewproducts.ProductVersion) (types.List, diag.Diagnostics) {
	var diagnostics diag.Diagnostics
	stageType := types.ObjectType{AttrTypes: versionStageAttributeTypes}
	values := make([]attr.Value, 0, len(versions))
	for _, version := range versions {
		stages := make([]attr.Value, 0, len(version.Stages))
		for _, stage := range version.Stages {
			value, stageDiagnostics := types.ObjectValue(versionStageAttributeTypes, map[string]attr.Value{
				"stage": types.StringValue(stage.Stage), "status": types.StringValue(stage.Status),
			})
			diagnostics.Append(stageDiagnostics...)
			stages = append(stages, value)
		}
		stageList, listDiagnostics := types.ListValue(stageType, stages)
		diagnostics.Append(listDiagnostics...)
		value, valueDiagnostics := types.ObjectValue(versionAttributeTypes, map[string]attr.Value{
			"version_id": types.StringValue(version.ID), "version_name": types.StringValue(version.Name),
			"version_type": types.StringValue(version.Type), "stages": stageList,
		})
		diagnostics.Append(valueDiagnostics...)
		values = append(values, value)
	}
	list, listDiagnostics := types.ListValue(types.ObjectType{AttrTypes: versionAttributeTypes}, values)
	diagnostics.Append(listDiagnostics...)
	return list, diagnostics
}
