package provider

import (
	"context"
	"net/url"
	"os"
	"strings"

	providercomponents "github.com/elva-labs/terraform-provider-vew/internal/provider/components"
	providerimages "github.com/elva-labs/terraform-provider-vew/internal/provider/images"
	providerpipelines "github.com/elva-labs/terraform-provider-vew/internal/provider/pipelines"
	providerrecipes "github.com/elva-labs/terraform-provider-vew/internal/provider/recipes"
	providerreleaseactions "github.com/elva-labs/terraform-provider-vew/internal/provider/releaseactions"
	"github.com/elva-labs/terraform-provider-vew/internal/providerdata"
	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	"github.com/elva-labs/terraform-provider-vew/internal/vew/components"
	"github.com/elva-labs/terraform-provider-vew/internal/vew/images"
	"github.com/elva-labs/terraform-provider-vew/internal/vew/pipelines"
	"github.com/elva-labs/terraform-provider-vew/internal/vew/recipes"
	"github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ provider.Provider = &vewProvider{}

type vewProvider struct {
	version string
}

type providerModel struct {
	APIURL       types.String `tfsdk:"api_url"`
	TokenURL     types.String `tfsdk:"token_url"`
	ClientID     types.String `tfsdk:"client_id"`
	ClientSecret types.String `tfsdk:"client_secret"`
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &vewProvider{version: version}
	}
}

func (p *vewProvider) Metadata(_ context.Context, _ provider.MetadataRequest, response *provider.MetadataResponse) {
	response.TypeName = "vew"
	response.Version = p.version
}

func (p *vewProvider) Schema(_ context.Context, _ provider.SchemaRequest, response *provider.SchemaResponse) {
	response.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"api_url":       schema.StringAttribute{Optional: true},
			"token_url":     schema.StringAttribute{Optional: true},
			"client_id":     schema.StringAttribute{Optional: true},
			"client_secret": schema.StringAttribute{Optional: true, Sensitive: true},
		},
	}
}

func (p *vewProvider) Configure(ctx context.Context, request provider.ConfigureRequest, response *provider.ConfigureResponse) {
	var model providerModel
	response.Diagnostics.Append(request.Config.Get(ctx, &model)...)
	if response.Diagnostics.HasError() {
		return
	}
	config, diagnostics := resolveProviderConfig(model, os.LookupEnv)
	response.Diagnostics.Append(diagnostics...)
	if response.Diagnostics.HasError() {
		return
	}
	transport, err := vew.NewTransport(config)
	if err != nil {
		response.Diagnostics.AddError("Unable to configure VEW client", "The VEW client could not be configured.")
		return
	}
	componentReadTransport, err := vew.NewTransportWithScopes(config, "clients/packaging/component.read")
	if err != nil {
		response.Diagnostics.AddError("Unable to configure VEW component read client", "The VEW component read client could not be configured.")
		return
	}
	recipeTransport, err := vew.NewTransportWithScopes(config, "clients/packaging/recipe.read", "clients/packaging/recipe.write")
	if err != nil {
		response.Diagnostics.AddError("Unable to configure VEW recipe client", "The VEW recipe client could not be configured.")
		return
	}
	recipeReadTransport, err := vew.NewTransportWithScopes(config, "clients/packaging/recipe.read")
	if err != nil {
		response.Diagnostics.AddError("Unable to configure VEW recipe read client", "The VEW recipe read client could not be configured.")
		return
	}
	componentReleaseTransport, err := vew.NewTransportWithScopes(config, "clients/packaging/component.release")
	if err != nil {
		response.Diagnostics.AddError("Unable to configure VEW component version release client", "The VEW component version release client could not be configured.")
		return
	}
	recipeReleaseTransport, err := vew.NewTransportWithScopes(config, "clients/packaging/recipe.release")
	if err != nil {
		response.Diagnostics.AddError("Unable to configure VEW recipe version release client", "The VEW recipe version release client could not be configured.")
		return
	}
	pipelineTransport, err := vew.NewTransportWithScopes(config, "clients/packaging/pipeline.read", "clients/packaging/pipeline.write")
	if err != nil {
		response.Diagnostics.AddError("Unable to configure VEW pipeline client", "The VEW pipeline client could not be configured.")
		return
	}
	pipelineReadTransport, err := vew.NewTransportWithScopes(config, "clients/packaging/pipeline.read")
	if err != nil {
		response.Diagnostics.AddError("Unable to configure VEW pipeline read client", "The VEW pipeline read client could not be configured.")
		return
	}
	api := components.NewClient(transport)
	componentReadAPI := components.NewClient(componentReadTransport)
	recipeAPI := recipes.NewClient(recipeTransport)
	recipeReadAPI := recipes.NewClient(recipeReadTransport)
	componentReleaseAPI := components.NewClient(componentReleaseTransport)
	recipeReleaseAPI := recipes.NewClient(recipeReleaseTransport)
	pipelineAPI := pipelines.NewClient(pipelineTransport)
	pipelineReadAPI := pipelines.NewClient(pipelineReadTransport)
	imageReadAPI := images.NewClient(pipelineReadTransport)
	data := providerdata.Data{
		Components:               api,
		ComponentReads:           componentReadAPI,
		ComponentVersions:        api,
		ComponentVersionReads:    componentReadAPI,
		ComponentVersionReleases: componentReleaseAPI,
		ImageReads:               imageReadAPI,
		Pipelines:                pipelineAPI,
		PipelineReads:            pipelineReadAPI,
		Recipes:                  recipeAPI,
		RecipeReads:              recipeReadAPI,
		RecipeVersions:           recipeAPI,
		RecipeVersionReads:       recipeReadAPI,
		RecipeVersionReleases:    recipeReleaseAPI,
		Waiter:                   vew.NewWaiter(),
	}
	response.ResourceData = data
	response.DataSourceData = data
	response.ActionData = data
}

func resolveProviderConfig(model providerModel, getenv func(string) (string, bool)) (vew.Config, diag.Diagnostics) {
	var diagnostics diag.Diagnostics
	values := []struct {
		name     string
		env      string
		value    types.String
		setValue func(*vew.Config, string)
	}{
		{"api_url", "VEW_API_URL", model.APIURL, func(config *vew.Config, value string) { config.APIURL = value }},
		{"token_url", "VEW_TOKEN_URL", model.TokenURL, func(config *vew.Config, value string) { config.TokenURL = value }},
		{"client_id", "VEW_CLIENT_ID", model.ClientID, func(config *vew.Config, value string) { config.ClientID = value }},
		{"client_secret", "VEW_CLIENT_SECRET", model.ClientSecret, func(config *vew.Config, value string) { config.ClientSecret = value }},
	}
	var config vew.Config
	for _, field := range values {
		if field.value.IsUnknown() {
			diagnostics.AddError("Unknown provider configuration", field.name+" must be known; configure it explicitly or through "+field.env+".")
			continue
		}
		value := ""
		if field.value.IsNull() == false {
			value = strings.TrimSpace(field.value.ValueString())
		}
		if value == "" {
			value, _ = getenv(field.env)
			value = strings.TrimSpace(value)
		}
		if value == "" {
			diagnostics.AddError("Missing provider configuration", "Set the "+field.name+" attribute or "+field.env+" environment variable.")
			continue
		}
		field.setValue(&config, value)
	}
	if config.APIURL != "" && !validHTTPURL(config.APIURL) {
		diagnostics.AddError("Invalid provider configuration", "api_url must be an absolute HTTP or HTTPS URL.")
	}
	if config.TokenURL != "" && !validHTTPURL(config.TokenURL) {
		diagnostics.AddError("Invalid provider configuration", "token_url must be an absolute HTTP or HTTPS URL.")
	}
	return config, diagnostics
}

func validHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Host != "" && (u.Scheme == "http" || u.Scheme == "https")
}

func (p *vewProvider) Resources(context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		func() resource.Resource { return providercomponents.NewComponentResource() },
		providercomponents.NewComponentVersionResource,
		providerpipelines.NewPipelineResource,
		providerrecipes.NewRecipeResource,
		providerrecipes.NewRecipeVersionResource,
	}
}

func (p *vewProvider) DataSources(context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		providercomponents.NewComponentDataSource,
		providercomponents.NewComponentVersionDataSource,
		providerrecipes.NewRecipeDataSource,
		providerrecipes.NewRecipeVersionDataSource,
		providerpipelines.NewPipelineDataSource,
		providerimages.NewImageDataSource,
		providerpipelines.NewPipelinesDataSource,
		providerimages.NewImagesDataSource,
	}
}

func (p *vewProvider) Actions(context.Context) []func() action.Action {
	return []func() action.Action{
		providerreleaseactions.NewComponentVersionReleaseAction,
		providerreleaseactions.NewRecipeVersionReleaseAction,
	}
}
