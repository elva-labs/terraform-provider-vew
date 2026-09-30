package projectaccounts

import (
	"context"
	"strings"

	"github.com/elva-labs/terraform-provider-vew/internal/providerdata"
	vewaccounts "github.com/elva-labs/terraform-provider-vew/internal/vew/projectaccounts"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource                   = (*accountsDataSource)(nil)
	_ datasource.DataSourceWithConfigure      = (*accountsDataSource)(nil)
	_ datasource.DataSourceWithValidateConfig = (*accountsDataSource)(nil)
)

type accountsDataSource struct {
	client vewaccounts.ListAPI
}

type accountsDataSourceModel struct {
	ProjectID types.String `tfsdk:"project_id"`
	Accounts  types.List   `tfsdk:"accounts"`
}

var accountAttributeTypes = map[string]attr.Type{
	"id": types.StringType, "aws_account_id": types.StringType, "account_type": types.StringType,
	"name": types.StringType, "technology_id": types.StringType, "stage": types.StringType, "region": types.StringType,
	"status": types.StringType, "last_onboarding_result": types.StringType, "onboarding_revision": types.StringType,
	"onboarded_at": types.StringType,
}

// NewProjectAccountsDataSource constructs the vew_project_accounts data source.
func NewProjectAccountsDataSource() datasource.DataSource { return &accountsDataSource{} }

func (d *accountsDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_project_accounts"
}

func (d *accountsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	computed := func(description string) schema.StringAttribute {
		return schema.StringAttribute{Computed: true, Description: description}
	}
	response.Schema = schema.Schema{
		Description: "Lists the AWS account assignments of a VEW project, including inactive ones, with their onboarding state.",
		Attributes: map[string]schema.Attribute{
			"project_id": schema.StringAttribute{Required: true, Description: "VEW project whose accounts are listed."},
			"accounts": schema.ListNestedAttribute{Computed: true, NestedObject: schema.NestedAttributeObject{
				Attributes: map[string]schema.Attribute{
					"id":                     computed("Internal VEW account assignment ID."),
					"aws_account_id":         computed("12-digit AWS account ID."),
					"account_type":           computed("USER or TOOLCHAIN."),
					"name":                   computed("Account assignment name."),
					"technology_id":          computed("Technology the account is onboarded for."),
					"stage":                  computed("dev, qa or prod."),
					"region":                 computed("AWS region."),
					"status":                 computed("Current status, e.g. OnBoarding, Active, ReOnboarding, Failed or Inactive."),
					"last_onboarding_result": computed("Succeeded or Failed, null before the first onboarding finished."),
					"onboarding_revision":    computed("Stored onboarding revision, null if none was set."),
					"onboarded_at": computed("When onboarding first succeeded, null if it never did. VEW sets it once and never " +
						"clears it, also not on deactivation, so it can gate resources that need an onboarded account."),
				},
			}},
		},
	}
}

func (d *accountsDataSource) Configure(_ context.Context, request datasource.ConfigureRequest, response *datasource.ConfigureResponse) {
	if request.ProviderData == nil {
		return
	}
	data, ok := request.ProviderData.(providerdata.Data)
	if !ok || strings.TrimSpace(data.ProjectAPIURL) == "" || data.ProjectAccountReads == nil {
		response.Diagnostics.AddError("Missing Projects API URL", "The vew_project_accounts data source requires a Projects API endpoint. Set projects_api_url or VEW_PROJECTS_API_URL.")
		return
	}
	d.client = data.ProjectAccountReads
}

func (d *accountsDataSource) ValidateConfig(ctx context.Context, request datasource.ValidateConfigRequest, response *datasource.ValidateConfigResponse) {
	var config accountsDataSourceModel
	response.Diagnostics.Append(request.Config.Get(ctx, &config)...)
	if response.Diagnostics.HasError() {
		return
	}
	if !config.ProjectID.IsNull() && !config.ProjectID.IsUnknown() && strings.TrimSpace(config.ProjectID.ValueString()) == "" {
		response.Diagnostics.AddAttributeError(path.Root("project_id"), "Empty project ID", "project_id must be non-empty.")
	}
}

func (d *accountsDataSource) Read(ctx context.Context, request datasource.ReadRequest, response *datasource.ReadResponse) {
	var model accountsDataSourceModel
	response.Diagnostics.Append(request.Config.Get(ctx, &model)...)
	if response.Diagnostics.HasError() {
		return
	}
	accounts, err := d.client.ListAccounts(ctx, model.ProjectID.ValueString())
	if err != nil {
		// A failed lookup must fail the plan: configurations gate resources on this list.
		addAccountError(&response.Diagnostics, "list", err, "")
		return
	}
	list, diagnostics := accountsValue(accounts)
	response.Diagnostics.Append(diagnostics...)
	if response.Diagnostics.HasError() {
		return
	}
	model.Accounts = list
	response.Diagnostics.Append(response.State.Set(ctx, &model)...)
}

func accountsValue(accounts []vewaccounts.Account) (types.List, diag.Diagnostics) {
	var diagnostics diag.Diagnostics
	values := make([]attr.Value, 0, len(accounts))
	for _, a := range accounts {
		value, valueDiagnostics := types.ObjectValue(accountAttributeTypes, map[string]attr.Value{
			"id": types.StringValue(a.ID), "aws_account_id": types.StringValue(a.AWSAccountID),
			"account_type": types.StringValue(a.AccountType), "name": types.StringValue(a.Name),
			"technology_id": types.StringValue(a.TechnologyID), "stage": types.StringValue(a.Stage),
			"region": types.StringValue(a.Region), "status": types.StringValue(a.Status),
			"last_onboarding_result": optionalString(a.LastOnboardingResult),
			"onboarding_revision":    optionalString(a.OnboardingRevision),
			"onboarded_at":           optionalString(a.OnboardedAt),
		})
		diagnostics.Append(valueDiagnostics...)
		values = append(values, value)
	}
	list, listDiagnostics := types.ListValue(types.ObjectType{AttrTypes: accountAttributeTypes}, values)
	diagnostics.Append(listDiagnostics...)
	return list, diagnostics
}
