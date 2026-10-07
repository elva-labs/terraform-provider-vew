package provider

import (
	"context"
	"net/url"
	"os"
	"strings"

	providerbaseimages "github.com/elva-labs/terraform-provider-vew/internal/provider/baseimages"
	providercomponents "github.com/elva-labs/terraform-provider-vew/internal/provider/components"
	providerimageactions "github.com/elva-labs/terraform-provider-vew/internal/provider/imageactions"
	providerimages "github.com/elva-labs/terraform-provider-vew/internal/provider/images"
	providermandatorycomponents "github.com/elva-labs/terraform-provider-vew/internal/provider/mandatorycomponents"
	providerpipelines "github.com/elva-labs/terraform-provider-vew/internal/provider/pipelines"
	providerproducts "github.com/elva-labs/terraform-provider-vew/internal/provider/products"
	providerprojectaccess "github.com/elva-labs/terraform-provider-vew/internal/provider/projectaccess"
	providerprojectaccounts "github.com/elva-labs/terraform-provider-vew/internal/provider/projectaccounts"
	providerprojectsettings "github.com/elva-labs/terraform-provider-vew/internal/provider/projectsettings"
	providerrecipes "github.com/elva-labs/terraform-provider-vew/internal/provider/recipes"
	providerreleaseactions "github.com/elva-labs/terraform-provider-vew/internal/provider/releaseactions"
	providertechnologies "github.com/elva-labs/terraform-provider-vew/internal/provider/technologies"
	"github.com/elva-labs/terraform-provider-vew/internal/providerdata"
	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	"github.com/elva-labs/terraform-provider-vew/internal/vew/baseimages"
	"github.com/elva-labs/terraform-provider-vew/internal/vew/components"
	"github.com/elva-labs/terraform-provider-vew/internal/vew/images"
	"github.com/elva-labs/terraform-provider-vew/internal/vew/mandatorycomponents"
	"github.com/elva-labs/terraform-provider-vew/internal/vew/pipelines"
	"github.com/elva-labs/terraform-provider-vew/internal/vew/products"
	"github.com/elva-labs/terraform-provider-vew/internal/vew/projectaccess"
	"github.com/elva-labs/terraform-provider-vew/internal/vew/projectaccounts"
	"github.com/elva-labs/terraform-provider-vew/internal/vew/projectsettings"
	"github.com/elva-labs/terraform-provider-vew/internal/vew/recipes"
	"github.com/elva-labs/terraform-provider-vew/internal/vew/technologies"
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
	APIURL                 types.String `tfsdk:"api_url"`
	ProjectsAPIURL         types.String `tfsdk:"projects_api_url"`
	PublishingURL          types.String `tfsdk:"publishing_api_url"`
	TokenURL               types.String `tfsdk:"token_url"`
	ClientID               types.String `tfsdk:"client_id"`
	ClientSecret           types.String `tfsdk:"client_secret"`
	ProjectClientBootstrap types.Bool   `tfsdk:"project_client_bootstrap"`
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
			"api_url":                  schema.StringAttribute{Optional: true},
			"projects_api_url":         schema.StringAttribute{Optional: true},
			"publishing_api_url":       schema.StringAttribute{Optional: true},
			"token_url":                schema.StringAttribute{Optional: true},
			"client_id":                schema.StringAttribute{Optional: true},
			"client_secret":            schema.StringAttribute{Optional: true, Sensitive: true},
			"project_client_bootstrap": schema.BoolAttribute{Optional: true, Description: "Enable the one-time vew_project_client_bootstrap action with separate platform recovery credentials. Defaults to false. Normal assignment resources must use an assigned management client."},
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
	imageExecuteTransport, err := vew.NewTransportWithScopes(config, "clients/packaging/pipeline.execute")
	if err != nil {
		response.Diagnostics.AddError("Unable to configure VEW image build client", "The VEW image build execute client could not be configured.")
		return
	}
	pipelineReadTransport, err := vew.NewTransportWithScopes(config, "clients/packaging/pipeline.read")
	if err != nil {
		response.Diagnostics.AddError("Unable to configure VEW image build client", "The VEW image build read client could not be configured.")
		return
	}
	var technologyAPI technologies.API
	var projectAccountAPI projectaccounts.API
	var projectAccountReads projectaccounts.ListAPI
	var projectAccessAPI projectaccess.API
	var projectBootstrapAPI projectaccess.BootstrapAPI
	var projectSettingsAPI projectsettings.API
	if config.ProjectAPIURL != "" {
		projectsConfig := config
		projectsConfig.APIURL = config.ProjectAPIURL
		technologyWriteTransport, err := vew.NewTransportWithScopes(projectsConfig, "clients/projects/technology.write")
		if err != nil {
			response.Diagnostics.AddError("Unable to configure VEW technology client", "The VEW technology write client could not be configured.")
			return
		}
		technologyReadTransport, err := vew.NewTransportWithScopes(projectsConfig, "clients/projects/technology.read")
		if err != nil {
			response.Diagnostics.AddError("Unable to configure VEW technology client", "The VEW technology read client could not be configured.")
			return
		}
		accountWriteTransport, err := vew.NewTransportWithScopes(projectsConfig, "clients/projects/account.write")
		if err != nil {
			response.Diagnostics.AddError("Unable to configure VEW project account client", "The VEW project account write client could not be configured.")
			return
		}
		accountReadTransport, err := vew.NewTransportWithScopes(projectsConfig, "clients/projects/account.read")
		if err != nil {
			response.Diagnostics.AddError("Unable to configure VEW project account client", "The VEW project account read client could not be configured.")
			return
		}
		technologyAPI = technologies.NewClient(technologyWriteTransport, technologyReadTransport)
		accountClient := projectaccounts.NewClient(accountWriteTransport, accountReadTransport)
		projectAccountAPI, projectAccountReads = accountClient, accountClient
		projectPair := func(scope string) (projectaccess.Pair, error) {
			write, err := vew.NewTransportWithScopes(projectsConfig, "clients/projects/"+scope+".write")
			if err != nil {
				return projectaccess.Pair{}, err
			}
			read, err := vew.NewTransportWithScopes(projectsConfig, "clients/projects/"+scope+".read")
			if err != nil {
				return projectaccess.Pair{}, err
			}
			return projectaccess.Pair{Write: write, Read: read}, nil
		}
		programPair, err := projectPair("program")
		if err != nil {
			response.Diagnostics.AddError("Unable to configure Projects client", "The Projects program client could not be configured.")
			return
		}
		userPair, err := projectPair("assignment")
		if err != nil {
			response.Diagnostics.AddError("Unable to configure Projects client", "The Projects assignment client could not be configured.")
			return
		}
		groupPair, err := projectPair("group_assignment")
		if err != nil {
			response.Diagnostics.AddError("Unable to configure Projects client", "The Projects group assignment client could not be configured.")
			return
		}
		clientPair, err := projectPair("client_assignment")
		if err != nil {
			response.Diagnostics.AddError("Unable to configure Projects client", "The Projects client assignment client could not be configured.")
			return
		}
		if config.ProjectClientBootstrap {
			bootstrapTransport, bootstrapErr := vew.NewTransportWithScopes(projectsConfig,
				"clients/projects/client_assignment.write",
				"clients/projects/client_assignment.bootstrap")
			if bootstrapErr != nil {
				response.Diagnostics.AddError("Unable to configure Projects bootstrap client", "The Projects client assignment bootstrap client could not be configured.")
				return
			}
			projectBootstrapAPI = projectaccess.NewBootstrapClient(bootstrapTransport)
		}
		projectAccessAPI = projectaccess.NewClient(programPair, userPair, groupPair, clientPair)
		projectSettingsAPI = projectsettings.NewClient(programPair.Write, programPair.Read)
	}
	var productAPI products.API
	var versionAPI *products.VersionClient
	if config.PublishingAPIURL != "" {
		publishingConfig := config
		publishingConfig.APIURL = config.PublishingAPIURL
		productWriteTransport, err := vew.NewTransportWithScopes(publishingConfig, "clients/publishing/product.write")
		if err != nil {
			response.Diagnostics.AddError("Unable to configure VEW product client", "The VEW product write client could not be configured.")
			return
		}
		productReadTransport, err := vew.NewTransportWithScopes(publishingConfig, "clients/publishing/product.read")
		if err != nil {
			response.Diagnostics.AddError("Unable to configure VEW product client", "The VEW product read client could not be configured.")
			return
		}
		versionPromoteTransport, err := vew.NewTransportWithScopes(publishingConfig, "clients/publishing/version.promote")
		if err != nil {
			response.Diagnostics.AddError("Unable to configure VEW product version client", "The VEW product version promote client could not be configured.")
			return
		}
		versionReadTransport, err := vew.NewTransportWithScopes(publishingConfig, "clients/publishing/version.read")
		if err != nil {
			response.Diagnostics.AddError("Unable to configure VEW product version client", "The VEW product version read client could not be configured.")
			return
		}
		productAPI = products.NewClient(productWriteTransport, productReadTransport)
		versionAPI = products.NewVersionClient(versionPromoteTransport, versionReadTransport)
	}
	baseImageWriteTransport, err := vew.NewTransportWithScopes(config, "clients/packaging/base_image.write")
	if err != nil {
		response.Diagnostics.AddError("Unable to configure VEW base image client", "The VEW base image write client could not be configured.")
		return
	}
	baseImageReadTransport, err := vew.NewTransportWithScopes(config, "clients/packaging/base_image.read")
	if err != nil {
		response.Diagnostics.AddError("Unable to configure VEW base image client", "The VEW base image read client could not be configured.")
		return
	}
	mandatoryComponentsWriteTransport, err := vew.NewTransportWithScopes(config, "clients/packaging/mandatory_components_list.write")
	if err != nil {
		response.Diagnostics.AddError("Unable to configure VEW mandatory components client", "The VEW mandatory components list write client could not be configured.")
		return
	}
	mandatoryComponentsReadTransport, err := vew.NewTransportWithScopes(config, "clients/packaging/mandatory_components_list.read")
	if err != nil {
		response.Diagnostics.AddError("Unable to configure VEW mandatory components client", "The VEW mandatory components list read client could not be configured.")
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
	imageAPI := images.NewClient(imageExecuteTransport, pipelineReadTransport)
	imageReadAPI := images.NewClient(nil, pipelineReadTransport)
	data := providerdata.Data{
		Components:               api,
		ComponentReads:           componentReadAPI,
		ComponentVersions:        api,
		ComponentVersionReads:    componentReadAPI,
		ComponentVersionReleases: componentReleaseAPI,
		Images:                   imageAPI,
		ImageReads:               imageReadAPI,
		ProjectAPIURL:            config.ProjectAPIURL,
		Technologies:             technologyAPI,
		ProjectAccounts:          projectAccountAPI,
		ProjectAccountReads:      projectAccountReads,
		ProjectAccess:            projectAccessAPI,
		ProjectBootstrap:         projectBootstrapAPI,
		ClientID:                 config.ClientID,
		ProjectClientBootstrap:   config.ProjectClientBootstrap,
		ProjectSettings:          projectSettingsAPI,
		BaseImages:               baseimages.NewClient(baseImageWriteTransport, baseImageReadTransport),
		MandatoryComponents:      mandatorycomponents.NewClient(mandatoryComponentsWriteTransport, mandatoryComponentsReadTransport),
		PublishingAPIURL:         config.PublishingAPIURL,
		Products:                 productAPI,
		Pipelines:                pipelineAPI,
		PipelineReads:            pipelineReadAPI,
		Recipes:                  recipeAPI,
		RecipeReads:              recipeReadAPI,
		RecipeVersions:           recipeAPI,
		RecipeVersionReads:       recipeReadAPI,
		RecipeVersionReleases:    recipeReleaseAPI,
		Waiter:                   vew.NewWaiter(),
	}
	if versionAPI != nil {
		data.Promotions, data.ProductVersionReads = versionAPI, versionAPI
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
	// Projects resources use a separate API base, but it is optional so existing
	// Packaging-only configurations continue to work. Projects resources can
	// diagnose its absence before making a request when they are configured.
	if model.ProjectsAPIURL.IsUnknown() {
		diagnostics.AddError("Unknown provider configuration", "projects_api_url must be known; configure it explicitly or through VEW_PROJECTS_API_URL.")
	} else {
		projectsAPIURL := ""
		if !model.ProjectsAPIURL.IsNull() {
			projectsAPIURL = strings.TrimSpace(model.ProjectsAPIURL.ValueString())
		}
		if projectsAPIURL == "" {
			projectsAPIURL, _ = getenv("VEW_PROJECTS_API_URL")
			projectsAPIURL = strings.TrimSpace(projectsAPIURL)
		}
		config.ProjectAPIURL = projectsAPIURL
	}
	if model.ProjectClientBootstrap.IsUnknown() {
		diagnostics.AddError("Unknown provider configuration", "project_client_bootstrap must be known before configuring the provider.")
	} else if !model.ProjectClientBootstrap.IsNull() {
		config.ProjectClientBootstrap = model.ProjectClientBootstrap.ValueBool()
	}
	// Publishing resources are optional in the same way.
	if model.PublishingURL.IsUnknown() {
		diagnostics.AddError("Unknown provider configuration", "publishing_api_url must be known; configure it explicitly or through VEW_PUBLISHING_API_URL.")
	} else {
		publishingAPIURL := ""
		if !model.PublishingURL.IsNull() {
			publishingAPIURL = strings.TrimSpace(model.PublishingURL.ValueString())
		}
		if publishingAPIURL == "" {
			publishingAPIURL, _ = getenv("VEW_PUBLISHING_API_URL")
			publishingAPIURL = strings.TrimSpace(publishingAPIURL)
		}
		config.PublishingAPIURL = publishingAPIURL
	}
	if config.APIURL != "" && !validHTTPURL(config.APIURL) {
		diagnostics.AddError("Invalid provider configuration", "api_url must be an absolute HTTP or HTTPS URL.")
	}
	if config.ProjectAPIURL != "" && !validHTTPURL(config.ProjectAPIURL) {
		diagnostics.AddError("Invalid provider configuration", "projects_api_url must be an absolute HTTP or HTTPS URL.")
	}
	if config.PublishingAPIURL != "" && !validHTTPURL(config.PublishingAPIURL) {
		diagnostics.AddError("Invalid provider configuration", "publishing_api_url must be an absolute HTTP or HTTPS URL.")
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
		providertechnologies.NewTechnologyResource,
		providerprojectaccounts.NewProjectAccountResource,
		providerprojectaccess.NewProjectResource,
		providerprojectaccess.NewUserResource,
		providerprojectaccess.NewGroupResource,
		providerprojectaccess.NewClientResource,
		providerprojectsettings.NewManagementResource,
		providerprojectsettings.NewWorkbenchLifecycleResource,
		providerbaseimages.NewReleaseResource,
		providermandatorycomponents.NewListResource,
		providerproducts.NewProductResource,
		providerproducts.NewProductVersionPromotionResource,
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
		providerproducts.NewProductVersionsDataSource,
		providerprojectaccounts.NewProjectAccountsDataSource,
	}
}

func (p *vewProvider) Actions(context.Context) []func() action.Action {
	return []func() action.Action{
		providerreleaseactions.NewComponentVersionReleaseAction,
		providerreleaseactions.NewRecipeVersionReleaseAction,
		providerimageactions.NewImageBuildAction,
		providerprojectaccess.NewBootstrapAction,
	}
}
