package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
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

func (p *vewProvider) Configure(context.Context, provider.ConfigureRequest, *provider.ConfigureResponse) {
}

func (p *vewProvider) Resources(context.Context) []func() resource.Resource {
	return nil
}

func (p *vewProvider) DataSources(context.Context) []func() datasource.DataSource {
	return nil
}
