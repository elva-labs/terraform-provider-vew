package pipelines

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/elva-labs/terraform-provider-vew/internal/providerdata"
	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	vewpipelines "github.com/elva-labs/terraform-provider-vew/internal/vew/pipelines"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource                   = (*pipelineDataSource)(nil)
	_ datasource.DataSourceWithConfigure      = (*pipelineDataSource)(nil)
	_ datasource.DataSourceWithValidateConfig = (*pipelineDataSource)(nil)
	_ datasource.DataSource                   = (*pipelinesDataSource)(nil)
	_ datasource.DataSourceWithConfigure      = (*pipelinesDataSource)(nil)
	_ datasource.DataSourceWithValidateConfig = (*pipelinesDataSource)(nil)
)

type pipelineDataSource struct {
	client vewpipelines.ReadAPI
}

type pipelinesDataSource struct {
	client vewpipelines.ReadAPI
}

type pipelineDataSourceModel struct {
	ProjectID               types.String `tfsdk:"project_id"`
	PipelineID              types.String `tfsdk:"pipeline_id"`
	Name                    types.String `tfsdk:"name"`
	Description             types.String `tfsdk:"description"`
	RecipeID                types.String `tfsdk:"recipe_id"`
	RecipeName              types.String `tfsdk:"recipe_name"`
	RecipeVersionID         types.String `tfsdk:"recipe_version_id"`
	RecipeVersionName       types.String `tfsdk:"recipe_version_name"`
	BuildInstanceTypes      types.List   `tfsdk:"build_instance_types"`
	Schedule                types.String `tfsdk:"schedule"`
	ProductID               types.String `tfsdk:"product_id"`
	Status                  types.String `tfsdk:"status"`
	DistributionConfigARN   types.String `tfsdk:"distribution_config_arn"`
	InfrastructureConfigARN types.String `tfsdk:"infrastructure_config_arn"`
	PipelineARN             types.String `tfsdk:"pipeline_arn"`
	CreatedAt               types.String `tfsdk:"created_at"`
	CreatedBy               types.String `tfsdk:"created_by"`
	UpdatedAt               types.String `tfsdk:"updated_at"`
	UpdatedBy               types.String `tfsdk:"updated_by"`
}

type pipelinesDataSourceModel struct {
	ProjectID types.String              `tfsdk:"project_id"`
	Pipelines []pipelineDataSourceModel `tfsdk:"pipelines"`
}

func NewPipelineDataSource() datasource.DataSource { return &pipelineDataSource{} }

func NewPipelinesDataSource() datasource.DataSource { return &pipelinesDataSource{} }

func (d *pipelineDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_pipeline"
}

func (d *pipelinesDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_pipelines"
}

func pipelineDataSourceAttributes(selectors bool) map[string]schema.Attribute {
	stringAttribute := func(selector bool) schema.StringAttribute {
		if selector {
			return schema.StringAttribute{Required: true}
		}
		return schema.StringAttribute{Computed: true}
	}
	return map[string]schema.Attribute{
		"project_id":                stringAttribute(selectors),
		"pipeline_id":               stringAttribute(selectors),
		"name":                      schema.StringAttribute{Computed: true},
		"description":               schema.StringAttribute{Computed: true},
		"recipe_id":                 schema.StringAttribute{Computed: true},
		"recipe_name":               schema.StringAttribute{Computed: true},
		"recipe_version_id":         schema.StringAttribute{Computed: true},
		"recipe_version_name":       schema.StringAttribute{Computed: true},
		"build_instance_types":      schema.ListAttribute{Computed: true, ElementType: types.StringType},
		"schedule":                  schema.StringAttribute{Computed: true},
		"product_id":                schema.StringAttribute{Computed: true},
		"status":                    schema.StringAttribute{Computed: true},
		"distribution_config_arn":   schema.StringAttribute{Computed: true},
		"infrastructure_config_arn": schema.StringAttribute{Computed: true},
		"pipeline_arn":              schema.StringAttribute{Computed: true},
		"created_at":                schema.StringAttribute{Computed: true},
		"created_by":                schema.StringAttribute{Computed: true},
		"updated_at":                schema.StringAttribute{Computed: true},
		"updated_by":                schema.StringAttribute{Computed: true},
	}
}

func (d *pipelineDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		Description: "Reads one existing project-scoped VEW pipeline without managing it.",
		Attributes:  pipelineDataSourceAttributes(true),
	}
}

func (d *pipelinesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		Description: "Lists existing project-scoped VEW pipelines in ascending pipeline ID order.",
		Attributes: map[string]schema.Attribute{
			"project_id": schema.StringAttribute{Required: true},
			"pipelines": schema.ListNestedAttribute{
				Computed: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: pipelineDataSourceAttributes(false),
				},
			},
		},
	}
}

func (d *pipelineDataSource) Configure(_ context.Context, request datasource.ConfigureRequest, response *datasource.ConfigureResponse) {
	d.client = configurePipelineReadAPI(request.ProviderData, &response.Diagnostics)
}

func (d *pipelinesDataSource) Configure(_ context.Context, request datasource.ConfigureRequest, response *datasource.ConfigureResponse) {
	d.client = configurePipelineReadAPI(request.ProviderData, &response.Diagnostics)
}

func configurePipelineReadAPI(providerData any, diagnostics interface{ AddError(string, string) }) vewpipelines.ReadAPI {
	if providerData == nil {
		return nil
	}
	data, ok := providerData.(providerdata.Data)
	if !ok || data.PipelineReads == nil {
		diagnostics.AddError("Missing Pipeline Read API", "Expected provider data to include a read-only PipelineReads API.")
		return nil
	}
	return data.PipelineReads
}

func (d *pipelineDataSource) ValidateConfig(ctx context.Context, request datasource.ValidateConfigRequest, response *datasource.ValidateConfigResponse) {
	var config pipelineDataSourceModel
	response.Diagnostics.Append(request.Config.Get(ctx, &config)...)
	if response.Diagnostics.HasError() {
		return
	}
	validateDataSourceSelector(&response.Diagnostics, "project_id", config.ProjectID, true)
	validateDataSourceSelector(&response.Diagnostics, "pipeline_id", config.PipelineID, true)
}

func (d *pipelinesDataSource) ValidateConfig(ctx context.Context, request datasource.ValidateConfigRequest, response *datasource.ValidateConfigResponse) {
	var config pipelinesDataSourceModel
	response.Diagnostics.Append(request.Config.Get(ctx, &config)...)
	if response.Diagnostics.HasError() {
		return
	}
	validateDataSourceSelector(&response.Diagnostics, "project_id", config.ProjectID, true)
}

func validateDataSourceSelector(diagnostics interface {
	AddAttributeError(path.Path, string, string)
}, name string, value types.String, allowUnknown bool) bool {
	if value.IsUnknown() {
		if allowUnknown {
			return true
		}
		diagnostics.AddAttributeError(path.Root(name), "Invalid pipeline data source selector", name+" must be known and nonempty without surrounding whitespace.")
		return false
	}
	if value.IsNull() || strings.TrimSpace(value.ValueString()) == "" || value.ValueString() != strings.TrimSpace(value.ValueString()) || !safeCorrelation.MatchString(value.ValueString()) {
		diagnostics.AddAttributeError(path.Root(name), "Invalid pipeline data source selector", name+" must be known and nonempty without surrounding whitespace.")
		return false
	}
	return true
}

func (d *pipelineDataSource) Read(ctx context.Context, request datasource.ReadRequest, response *datasource.ReadResponse) {
	var state pipelineDataSourceModel
	response.Diagnostics.Append(request.Config.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	if !validateDataSourceSelector(&response.Diagnostics, "project_id", state.ProjectID, false) || !validateDataSourceSelector(&response.Diagnostics, "pipeline_id", state.PipelineID, false) {
		return
	}
	if d.client == nil {
		response.Diagnostics.AddError("Unable to read VEW pipeline", "The read-only pipeline client is not configured.")
		return
	}
	remote, err := d.client.GetPipeline(ctx, state.ProjectID.ValueString(), state.PipelineID.ValueString())
	if err != nil {
		addPipelineDataSourceError(&response.Diagnostics, "read", err, state.ProjectID.ValueString(), state.PipelineID.ValueString())
		return
	}
	mapped, err := mapPipelineDataSource(ctx, state.ProjectID.ValueString(), remote)
	if err != nil {
		addPipelineDataSourceError(&response.Diagnostics, "read", err, state.ProjectID.ValueString(), state.PipelineID.ValueString())
		return
	}
	if mapped.PipelineID.ValueString() != state.PipelineID.ValueString() {
		addPipelineDataSourceError(&response.Diagnostics, "read", errors.New("pipeline response ID differs from requested pipeline"), state.ProjectID.ValueString(), state.PipelineID.ValueString())
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &mapped)...)
}

func (d *pipelinesDataSource) Read(ctx context.Context, request datasource.ReadRequest, response *datasource.ReadResponse) {
	var state pipelinesDataSourceModel
	response.Diagnostics.Append(request.Config.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	if !validateDataSourceSelector(&response.Diagnostics, "project_id", state.ProjectID, false) {
		return
	}
	if d.client == nil {
		response.Diagnostics.AddError("Unable to list VEW pipelines", "The read-only pipeline client is not configured.")
		return
	}
	remotes, err := d.client.ListPipelines(ctx, state.ProjectID.ValueString())
	if err != nil {
		addPipelineDataSourceError(&response.Diagnostics, "list", err, state.ProjectID.ValueString(), "")
		return
	}
	mapped, mapErr := mapPipelineCollection(ctx, state.ProjectID.ValueString(), remotes)
	if mapErr != nil {
		addPipelineDataSourceError(&response.Diagnostics, "list", mapErr, state.ProjectID.ValueString(), "")
		return
	}
	state.Pipelines = mapped
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

func mapPipelineCollection(ctx context.Context, requestedProjectID string, remotes []vewpipelines.Pipeline) ([]pipelineDataSourceModel, error) {
	ordered := append([]vewpipelines.Pipeline(nil), remotes...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	mapped := make([]pipelineDataSourceModel, 0, len(ordered))
	for _, remote := range ordered {
		item, err := mapPipelineDataSource(ctx, requestedProjectID, remote)
		if err != nil {
			return nil, err
		}
		mapped = append(mapped, item)
	}
	return mapped, nil
}

func mapPipelineDataSource(ctx context.Context, requestedProjectID string, remote vewpipelines.Pipeline) (pipelineDataSourceModel, error) {
	if strings.TrimSpace(remote.ID) == "" {
		return pipelineDataSourceModel{}, errors.New("pipeline response missing ID")
	}
	if remote.ProjectID != "" && remote.ProjectID != requestedProjectID {
		return pipelineDataSourceModel{}, errors.New("pipeline response project ID differs from requested project")
	}
	instances, diagnostics := types.ListValueFrom(ctx, types.StringType, remote.BuildInstanceTypes)
	if diagnostics.HasError() {
		return pipelineDataSourceModel{}, errors.New("pipeline response has invalid build instance types")
	}
	return pipelineDataSourceModel{
		ProjectID:               types.StringValue(requestedProjectID),
		PipelineID:              types.StringValue(remote.ID),
		Name:                    types.StringValue(remote.Name),
		Description:             types.StringValue(remote.Description),
		RecipeID:                types.StringValue(remote.RecipeID),
		RecipeName:              types.StringValue(remote.RecipeName),
		RecipeVersionID:         types.StringValue(remote.RecipeVersionID),
		RecipeVersionName:       types.StringValue(remote.RecipeVersionName),
		BuildInstanceTypes:      instances,
		Schedule:                types.StringValue(remote.Schedule),
		ProductID:               nullableString(remote.ProductID),
		Status:                  types.StringValue(remote.Status),
		DistributionConfigARN:   nullableString(remote.DistributionConfigARN),
		InfrastructureConfigARN: nullableString(remote.InfrastructureConfigARN),
		PipelineARN:             nullableString(remote.PipelineARN),
		CreatedAt:               types.StringValue(remote.CreatedAt),
		CreatedBy:               types.StringValue(remote.CreatedBy),
		UpdatedAt:               types.StringValue(remote.UpdatedAt),
		UpdatedBy:               types.StringValue(remote.UpdatedBy),
	}, nil
}

func addPipelineDataSourceError(diagnostics interface{ AddError(string, string) }, operation string, err error, projectID, pipelineID string) {
	if vew.IsNotFound(err) && pipelineID != "" {
		detail := "The requested VEW pipeline was not found."
		if safeCorrelation.MatchString(projectID) && safeCorrelation.MatchString(pipelineID) {
			detail = fmt.Sprintf("VEW pipeline %s was not found in project %s.", pipelineID, projectID)
		}
		diagnostics.AddError("VEW pipeline not found", detail)
		return
	}
	detail := "VEW pipeline data source " + operation + " failed."
	var api *vew.APIError
	if errors.As(err, &api) {
		detail = fmt.Sprintf("VEW pipeline data source %s failed (HTTP status %d).", operation, api.Status)
		if safeCorrelation.MatchString(api.Problem.Code) {
			detail += " VEW code: " + api.Problem.Code + "."
		}
		if safeCorrelation.MatchString(api.Problem.RequestID) {
			detail += " Request ID: " + api.Problem.RequestID + "."
		}
	}
	if safeCorrelation.MatchString(projectID) {
		detail += " Project ID: " + projectID + "."
	}
	if safeCorrelation.MatchString(pipelineID) {
		detail += " Pipeline ID: " + pipelineID + "."
	}
	diagnostics.AddError("Unable to "+operation+" VEW pipelines", detail)
}
