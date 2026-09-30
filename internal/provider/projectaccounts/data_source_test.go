package projectaccounts

import (
	"context"
	"errors"
	"testing"

	vewaccounts "github.com/elva-labs/terraform-provider-vew/internal/vew/projectaccounts"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

type listStub struct {
	accounts []vewaccounts.Account
	err      error
	project  string
}

func (s *listStub) ListAccounts(_ context.Context, projectID string) ([]vewaccounts.Account, error) {
	s.project = projectID
	return s.accounts, s.err
}

func readAccounts(t *testing.T, stub *listStub) (accountsDataSourceModel, datasource.ReadResponse) {
	t.Helper()
	d := &accountsDataSource{client: stub}
	var schemaResponse datasource.SchemaResponse
	d.Schema(context.Background(), datasource.SchemaRequest{}, &schemaResponse)
	objectType := schemaResponse.Schema.Type().TerraformType(context.Background())
	config := tfsdk.Config{Schema: schemaResponse.Schema, Raw: tftypes.NewValue(objectType, map[string]tftypes.Value{
		"project_id": tftypes.NewValue(tftypes.String, "project-1"),
		"accounts":   tftypes.NewValue(objectType.(tftypes.Object).AttributeTypes["accounts"], nil),
	})}
	response := datasource.ReadResponse{State: tfsdk.State{Schema: schemaResponse.Schema}}
	d.Read(context.Background(), datasource.ReadRequest{Config: config}, &response)
	var model accountsDataSourceModel
	if !response.Diagnostics.HasError() {
		if diagnostics := response.State.Get(context.Background(), &model); diagnostics.HasError() {
			t.Fatal(diagnostics)
		}
	}
	return model, response
}

func TestProjectAccountsDataSourceListsOnboardingState(t *testing.T) {
	onboarded := testAccount("account-1", "Inactive", "Failed")
	onboarded.OnboardedAt = "2026-09-30T12:00:00+00:00"
	onboarded.OnboardingRevision = "r1"
	pending := testAccount("account-2", "OnBoarding", "")
	stub := &listStub{accounts: []vewaccounts.Account{onboarded, pending}}

	model, response := readAccounts(t, stub)
	if response.Diagnostics.HasError() {
		t.Fatal(response.Diagnostics)
	}
	if stub.project != "project-1" {
		t.Fatalf("listed project %q", stub.project)
	}
	var accounts []struct {
		ID                   types.String `tfsdk:"id"`
		AWSAccountID         types.String `tfsdk:"aws_account_id"`
		AccountType          types.String `tfsdk:"account_type"`
		Name                 types.String `tfsdk:"name"`
		TechnologyID         types.String `tfsdk:"technology_id"`
		Stage                types.String `tfsdk:"stage"`
		Region               types.String `tfsdk:"region"`
		Status               types.String `tfsdk:"status"`
		LastOnboardingResult types.String `tfsdk:"last_onboarding_result"`
		OnboardingRevision   types.String `tfsdk:"onboarding_revision"`
		OnboardedAt          types.String `tfsdk:"onboarded_at"`
	}
	if diagnostics := model.Accounts.ElementsAs(context.Background(), &accounts, false); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	if len(accounts) != 2 {
		t.Fatalf("accounts=%d", len(accounts))
	}
	// An inactive account keeps onboarded_at: it gates "was onboarded", not "is active".
	if accounts[0].OnboardedAt.ValueString() != "2026-09-30T12:00:00+00:00" || accounts[0].OnboardingRevision.ValueString() != "r1" || accounts[0].Status.ValueString() != "Inactive" {
		t.Fatalf("first=%#v", accounts[0])
	}
	if !accounts[1].OnboardedAt.IsNull() || !accounts[1].LastOnboardingResult.IsNull() || !accounts[1].OnboardingRevision.IsNull() {
		t.Fatalf("pending account must have null onboarding fields: %#v", accounts[1])
	}
}

func TestProjectAccountsDataSourceFailsThePlanWhenTheLookupFails(t *testing.T) {
	_, response := readAccounts(t, &listStub{err: errors.New("boom")})
	if !response.Diagnostics.HasError() {
		t.Fatal("a failed lookup must be an error, never an empty list")
	}
}
