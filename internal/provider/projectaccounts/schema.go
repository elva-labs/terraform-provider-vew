package projectaccounts

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

type model struct {
	ID                   types.String `tfsdk:"id"`
	ProjectID            types.String `tfsdk:"project_id"`
	AWSAccountID         types.String `tfsdk:"aws_account_id"`
	AccountType          types.String `tfsdk:"account_type"`
	Name                 types.String `tfsdk:"name"`
	Description          types.String `tfsdk:"description"`
	TechnologyID         types.String `tfsdk:"technology_id"`
	Stage                types.String `tfsdk:"stage"`
	Region               types.String `tfsdk:"region"`
	Status               types.String `tfsdk:"status"`
	LastOnboardingResult types.String `tfsdk:"last_onboarding_result"`
	LastOnboardingError  types.String `tfsdk:"last_onboarding_error"`
	CreatedAt            types.String `tfsdk:"created_at"`
	UpdatedAt            types.String `tfsdk:"updated_at"`
	Timeouts             types.Object `tfsdk:"timeouts"`
}

type timeoutModel struct {
	Create types.String `tfsdk:"create"`
	Update types.String `tfsdk:"update"`
}

var timeoutAttrTypes = map[string]attr.Type{"create": types.StringType, "update": types.StringType}

func requiredString(replace bool) schema.StringAttribute {
	a := schema.StringAttribute{Required: true}
	if replace {
		a.PlanModifiers = []planmodifier.String{stringplanmodifier.RequiresReplace()}
	}
	return a
}

func resourceSchema() schema.Schema {
	return schema.Schema{Attributes: map[string]schema.Attribute{
		"id":         schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		"project_id": requiredString(true), "aws_account_id": requiredString(true),
		"account_type": requiredString(false), "name": requiredString(false), "description": requiredString(false),
		"technology_id": requiredString(false), "stage": requiredString(false), "region": requiredString(false),
		"status":                 schema.StringAttribute{Computed: true},
		"last_onboarding_result": schema.StringAttribute{Computed: true},
		"last_onboarding_error":  schema.StringAttribute{Computed: true, Sensitive: true},
		"created_at":             schema.StringAttribute{Computed: true}, "updated_at": schema.StringAttribute{Computed: true},
		"timeouts": schema.SingleNestedAttribute{Optional: true, Attributes: map[string]schema.Attribute{
			"create": schema.StringAttribute{Optional: true, Description: "Timeout for account onboarding. Defaults to 2h."},
			"update": schema.StringAttribute{Optional: true, Description: "Timeout for account re-onboarding. Defaults to 2h."},
		}},
	}}
}

func validateModel(ctx context.Context, m model) diag.Diagnostics {
	var d diag.Diagnostics
	for name, value := range map[string]types.String{
		"project_id": m.ProjectID, "aws_account_id": m.AWSAccountID, "account_type": m.AccountType,
		"name": m.Name, "description": m.Description, "technology_id": m.TechnologyID, "stage": m.Stage, "region": m.Region,
	} {
		if value.IsNull() || value.IsUnknown() {
			continue
		}
		if strings.TrimSpace(value.ValueString()) == "" {
			d.AddAttributeError(path.Root(name), "Empty project account value", name+" must be non-empty.")
		}
	}
	if !m.AWSAccountID.IsNull() && !m.AWSAccountID.IsUnknown() && (len(m.AWSAccountID.ValueString()) != 12 || !allDigits(m.AWSAccountID.ValueString())) {
		d.AddAttributeError(path.Root("aws_account_id"), "Invalid AWS account ID", "aws_account_id must contain exactly 12 digits.")
	}
	if !m.AccountType.IsNull() && !m.AccountType.IsUnknown() && m.AccountType.ValueString() != "USER" && m.AccountType.ValueString() != "TOOLCHAIN" {
		d.AddAttributeError(path.Root("account_type"), "Invalid account type", "account_type must be USER or TOOLCHAIN.")
	}
	if !m.Stage.IsNull() && !m.Stage.IsUnknown() && m.Stage.ValueString() != "dev" && m.Stage.ValueString() != "qa" && m.Stage.ValueString() != "prod" {
		d.AddAttributeError(path.Root("stage"), "Invalid account stage", "stage must be dev, qa, or prod.")
	}
	if !m.Region.IsNull() && !m.Region.IsUnknown() && !awsRegion.MatchString(m.Region.ValueString()) {
		d.AddAttributeError(path.Root("region"), "Invalid AWS region", "region must be a valid AWS region identifier, such as eu-west-1.")
	}
	d.Append(validateTimeouts(ctx, m.Timeouts)...)
	return d
}

func validateBeforeRequest(ctx context.Context, m model) diag.Diagnostics {
	d := validateModel(ctx, m)
	for name, value := range map[string]types.String{
		"project_id": m.ProjectID, "aws_account_id": m.AWSAccountID, "account_type": m.AccountType,
		"name": m.Name, "description": m.Description, "technology_id": m.TechnologyID, "stage": m.Stage, "region": m.Region,
	} {
		if value.IsNull() || value.IsUnknown() {
			d.AddAttributeError(path.Root(name), "Unknown project account value", name+" must be known before an account request is sent.")
		}
	}
	return d
}

func allDigits(value string) bool {
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func validateTimeouts(ctx context.Context, value types.Object) diag.Diagnostics {
	var d diag.Diagnostics
	if value.IsNull() || value.IsUnknown() {
		return d
	}
	var t timeoutModel
	d.Append(value.As(ctx, &t, basetypes.ObjectAsOptions{})...)
	if d.HasError() {
		return d
	}
	for name, field := range map[string]types.String{"create": t.Create, "update": t.Update} {
		if field.IsNull() || field.IsUnknown() {
			continue
		}
		duration, err := time.ParseDuration(field.ValueString())
		if err != nil || duration <= 0 {
			d.AddAttributeError(path.Root("timeouts").AtName(name), "Invalid project account timeout", fmt.Sprintf("timeouts.%s must be a positive Go duration.", name))
		}
	}
	return d
}

func operationTimeout(ctx context.Context, value types.Object, operation string) time.Duration {
	if value.IsNull() || value.IsUnknown() {
		return 2 * time.Hour
	}
	var t timeoutModel
	if value.As(ctx, &t, basetypes.ObjectAsOptions{}).HasError() {
		return 2 * time.Hour
	}
	field := t.Create
	if operation == "update" {
		field = t.Update
	}
	if field.IsNull() || field.IsUnknown() {
		return 2 * time.Hour
	}
	d, err := time.ParseDuration(field.ValueString())
	if err != nil || d <= 0 {
		return 2 * time.Hour
	}
	return d
}
