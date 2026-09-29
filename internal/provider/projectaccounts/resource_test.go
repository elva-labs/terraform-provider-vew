package projectaccounts

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	vewaccounts "github.com/elva-labs/terraform-provider-vew/internal/vew/projectaccounts"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type accountStub struct {
	account         vewaccounts.Account
	reads           []vewaccounts.Account
	readErr         error
	createAction    vewaccounts.ActionResult
	createErr       error
	createCalls     int
	createKey       string
	createInput     vewaccounts.AccountInput
	updateAction    vewaccounts.ActionResult
	updateErr       error
	updateCalls     int
	updateInput     vewaccounts.UpdateAccountInput
	deactivateCalls int
	deactivateErr   error
}

func (f *accountStub) CreateAccount(_ context.Context, _ string, input vewaccounts.AccountInput, key string) (vewaccounts.ActionResult, error) {
	f.createCalls++
	f.createInput = input
	f.createKey = key
	return f.createAction, f.createErr
}
func (f *accountStub) GetAccount(context.Context, string, string) (vewaccounts.Account, error) {
	if len(f.reads) != 0 {
		f.account = f.reads[0]
		if len(f.reads) > 1 {
			f.reads = f.reads[1:]
		}
	}
	return f.account, f.readErr
}
func (f *accountStub) UpdateAccount(_ context.Context, _, _ string, input vewaccounts.UpdateAccountInput) (vewaccounts.ActionResult, error) {
	f.updateCalls++
	f.updateInput = input
	return f.updateAction, f.updateErr
}
func (f *accountStub) DeactivateAccount(context.Context, string, string) error {
	f.deactivateCalls++
	return f.deactivateErr
}

type instantWaiter struct {
	timeout, delay time.Duration
	err            error
}

func (w *instantWaiter) Until(_ context.Context, timeout, delay time.Duration, read vew.StatusReader, evaluate vew.StatusEvaluator) error {
	w.timeout, w.delay = timeout, delay
	if w.err != nil {
		return w.err
	}
	result, err := read(context.Background())
	if err != nil {
		return err
	}
	_, err = evaluate(result.Status)
	return err
}

func testAccount(id, status, result string) vewaccounts.Account {
	return vewaccounts.Account{ID: id, ProjectID: "project-1", AWSAccountID: "000000000000", Name: "Build", Description: "test", AccountType: "USER", TechnologyID: "technology-1", Stage: "dev", Region: "eu-west-1", Status: status, LastOnboardingResult: result, LastOnboardingErrorMessage: "token=secret raw workflow detail", CreatedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)}
}

func validModel() model {
	return model{ID: types.StringNull(), ProjectID: types.StringValue("project-1"), AWSAccountID: types.StringValue("000000000000"), AccountType: types.StringValue("USER"), Name: types.StringValue("Build"), Description: types.StringValue("test"), TechnologyID: types.StringValue("technology-1"), Stage: types.StringValue("dev"), Region: types.StringValue("eu-west-1"), Status: types.StringNull(), LastOnboardingResult: types.StringNull(), LastOnboardingError: types.StringNull(), CreatedAt: types.StringNull(), UpdatedAt: types.StringNull(), Timeouts: types.ObjectNull(timeoutAttrTypes)}
}

func stateFor(t *testing.T, m model) tfsdk.State {
	t.Helper()
	var sr resource.SchemaResponse
	NewProjectAccountResource().Schema(context.Background(), resource.SchemaRequest{}, &sr)
	s := tfsdk.State{Schema: sr.Schema}
	if d := s.Set(context.Background(), &m); d.HasError() {
		t.Fatalf("state encode: %v", d)
	}
	return s
}

func TestSchemaReplacementAndTimeoutDefaults(t *testing.T) {
	var response resource.SchemaResponse
	NewProjectAccountResource().Schema(context.Background(), resource.SchemaRequest{}, &response)
	for _, name := range []string{"project_id", "aws_account_id"} {
		attribute, ok := response.Schema.Attributes[name].(schema.StringAttribute)
		if !ok {
			t.Fatalf("missing %s", name)
		}
		if !attribute.Required || len(attribute.PlanModifiers) != 1 {
			t.Fatalf("%s must be required and require replacement", name)
		}
	}
	mutable, ok := response.Schema.Attributes["name"].(schema.StringAttribute)
	if !ok || !mutable.Required || len(mutable.PlanModifiers) != 0 {
		t.Fatal("name must be required and updateable in place")
	}
	if got := operationTimeout(context.Background(), types.ObjectNull(timeoutAttrTypes), "create"); got != 2*time.Hour {
		t.Fatalf("create timeout=%s", got)
	}
	if got := operationTimeout(context.Background(), types.ObjectNull(timeoutAttrTypes), "update"); got != 2*time.Hour {
		t.Fatalf("update timeout=%s", got)
	}
}

func TestValidateModelRejectsInvalidAttributes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*model)
	}{
		{"account id", func(m *model) { m.AWSAccountID = types.StringValue("123") }},
		{"account type", func(m *model) { m.AccountType = types.StringValue("ADMIN") }},
		{"stage", func(m *model) { m.Stage = types.StringValue("staging") }},
		{"region", func(m *model) { m.Region = types.StringValue("not-a-region") }},
		{"region suffix", func(m *model) { m.Region = types.StringValue("eu-west-10") }},
		{"empty technology", func(m *model) { m.TechnologyID = types.StringValue(" ") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := validModel()
			tc.mutate(&m)
			if d := validateModel(context.Background(), m); !d.HasError() {
				t.Fatal("expected validation diagnostic")
			}
		})
	}
	for _, good := range []string{"USER", "TOOLCHAIN"} {
		m := validModel()
		m.AccountType = types.StringValue(good)
		if d := validateModel(context.Background(), m); d.HasError() {
			t.Fatalf("valid account type %s: %v", good, d)
		}
	}
	for _, region := range []string{"eu-west-1", "us-gov-west-1", "us-iso-east-1"} {
		m := validModel()
		m.Region = types.StringValue(region)
		if d := validateModel(context.Background(), m); d.HasError() {
			t.Fatalf("valid region %s: %v", region, d)
		}
	}
}

func TestCreatePersistsAcceptedIDBeforeWaitingAndHonorsRetryAfter(t *testing.T) {
	api := &accountStub{createAction: vewaccounts.ActionResult{ID: "internal-account", RetryAfter: 7 * time.Second}, account: testAccount("internal-account", "Active", "Succeeded")}
	waiter := &instantWaiter{}
	r := &projectAccountResource{client: api, waiter: waiter, projectsAPIURL: "https://projects.example"}
	m := validModel()
	state := stateFor(t, m)
	resp := resource.CreateResponse{State: state, Private: nil}
	r.Create(context.Background(), resource.CreateRequest{Plan: tfsdk.Plan{Schema: state.Schema, Raw: state.Raw}}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("create diagnostics: %v", resp.Diagnostics)
	}
	var got model
	if d := resp.State.Get(context.Background(), &got); d.HasError() {
		t.Fatal(d)
	}
	if got.ID.ValueString() != "internal-account" || got.Status.ValueString() != "Active" || got.LastOnboardingResult.ValueString() != "Succeeded" {
		t.Fatalf("state=%#v", got)
	}
	if api.createCalls != 1 || api.createInput.AWSAccountID != "000000000000" || !validUUID(api.createKey) || waiter.timeout != 2*time.Hour || waiter.delay != 7*time.Second {
		t.Fatalf("create/wait details: %#v %#v", api, waiter)
	}
	if got.LastOnboardingError.ValueString() != "" {
		t.Fatalf("unexpected error context: %q", got.LastOnboardingError.ValueString())
	}
}

func TestCreateFailureRetainsAcceptedIdentityAndSanitizesWorkflowError(t *testing.T) {
	api := &accountStub{createAction: vewaccounts.ActionResult{ID: "internal-account"}, account: testAccount("internal-account", "Failed", "Failed")}
	r := &projectAccountResource{client: api, waiter: &instantWaiter{}, projectsAPIURL: "https://projects.example"}
	m := validModel()
	state := stateFor(t, m)
	resp := resource.CreateResponse{State: state, Private: nil}
	r.Create(context.Background(), resource.CreateRequest{Plan: tfsdk.Plan{Schema: state.Schema, Raw: state.Raw}}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected onboarding failure")
	}
	var got model
	if d := resp.State.Get(context.Background(), &got); d.HasError() {
		t.Fatal(d)
	}
	if got.ID.ValueString() != "internal-account" || got.Status.ValueString() != "Failed" {
		t.Fatalf("recoverable state=%#v", got)
	}
	if strings.Contains(got.LastOnboardingError.ValueString(), "secret") || strings.Contains(resp.Diagnostics[0].Detail(), "secret") {
		t.Fatalf("sensitive workflow detail leaked: %v / %q", resp.Diagnostics, got.LastOnboardingError.ValueString())
	}
}

func TestReadRemovesInactiveAndDeleteOnlyDeactivates(t *testing.T) {
	api := &accountStub{account: testAccount("internal-account", "Inactive", "Succeeded")}
	r := &projectAccountResource{client: api, waiter: &instantWaiter{}, projectsAPIURL: "https://projects.example"}
	m := validModel()
	m.ID = types.StringValue("internal-account")
	state := stateFor(t, m)
	read := resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &read)
	if read.Diagnostics.HasError() {
		t.Fatal(read.Diagnostics)
	}
	if !read.State.Raw.IsNull() {
		t.Fatal("inactive account was retained in state")
	}
	api.account = testAccount("internal-account", "Active", "Succeeded")
	m.Status = types.StringValue("Active")
	state = stateFor(t, m)
	deleteResponse := resource.DeleteResponse{}
	r.Delete(context.Background(), resource.DeleteRequest{State: state}, &deleteResponse)
	if deleteResponse.Diagnostics.HasError() {
		t.Fatal(deleteResponse.Diagnostics)
	}
	if len(deleteResponse.Diagnostics) != 1 || deleteResponse.Diagnostics[0].Severity() != diag.SeverityWarning {
		t.Fatalf("expected safe retained-reference warning, got %v", deleteResponse.Diagnostics)
	}
	warning := deleteResponse.Diagnostics[0].Detail()
	if !strings.Contains(warning, "may prevent technology deletion") || strings.Contains(warning, "internal-account") || strings.Contains(warning, "secret") {
		t.Fatalf("warning was not actionable and safe: %q", warning)
	}
	if api.deactivateCalls != 1 {
		t.Fatalf("deactivation calls=%d", api.deactivateCalls)
	}
	api.account = testAccount("internal-account", "Inactive", "Succeeded")
	deleteResponse = resource.DeleteResponse{}
	r.Delete(context.Background(), resource.DeleteRequest{State: state}, &deleteResponse)
	if len(deleteResponse.Diagnostics) != 1 || deleteResponse.Diagnostics[0].Severity() != diag.SeverityWarning || api.deactivateCalls != 1 {
		t.Fatalf("already-inactive delete should warn without mutation: diagnostics=%v deactivations=%d", deleteResponse.Diagnostics, api.deactivateCalls)
	}
}

func TestUpdateReOnboardsAndRetainsPriorDesiredFieldsOnFailure(t *testing.T) {
	for _, tc := range []struct {
		name     string
		terminal bool
	}{{"success", false}, {"failed", true}} {
		t.Run(tc.name, func(t *testing.T) {
			initial := testAccount("internal-account", "Active", "Succeeded")
			finished := testAccount("internal-account", "Active", "Succeeded")
			finished.Name, finished.TechnologyID = "Updated", "technology-2"
			if tc.terminal {
				finished.LastOnboardingResult = "Failed"
				finished.LastOnboardingErrorMessage = "raw secret=do-not-leak"
			}
			api := &accountStub{reads: []vewaccounts.Account{initial, finished}, updateAction: vewaccounts.ActionResult{ID: "internal-account", RetryAfter: 4 * time.Second}}
			waiter := &instantWaiter{}
			r := &projectAccountResource{client: api, waiter: waiter, projectsAPIURL: "https://projects.example"}
			prior := validModel()
			prior.ID = types.StringValue("internal-account")
			prior.Status = types.StringValue("Active")
			prior.LastOnboardingResult = types.StringValue("Succeeded")
			plan := prior
			plan.Name = types.StringValue("Updated")
			plan.TechnologyID = types.StringValue("technology-2")
			priorState, planState := stateFor(t, prior), stateFor(t, plan)
			response := resource.UpdateResponse{State: priorState}
			r.Update(context.Background(), resource.UpdateRequest{State: priorState, Plan: tfsdk.Plan{Schema: planState.Schema, Raw: planState.Raw}}, &response)
			if response.Diagnostics.HasError() != tc.terminal {
				t.Fatalf("diagnostics=%v", response.Diagnostics)
			}
			var got model
			if d := response.State.Get(context.Background(), &got); d.HasError() {
				t.Fatal(d)
			}
			if api.updateCalls != 1 || api.updateInput.TechnologyID != "technology-2" || waiter.timeout != 2*time.Hour || waiter.delay != 4*time.Second {
				t.Fatalf("update/wait details: %#v %#v", api, waiter)
			}
			if tc.terminal {
				if got.Name.ValueString() != "Build" || got.TechnologyID.ValueString() != "technology-1" || got.ID.ValueString() != "internal-account" {
					t.Fatalf("failure state did not preserve retry diff: %#v", got)
				}
				if strings.Contains(response.Diagnostics[0].Detail(), "do-not-leak") {
					t.Fatalf("leaked onboarding error: %v", response.Diagnostics)
				}
			} else if got.Name.ValueString() != "Updated" || got.TechnologyID.ValueString() != "technology-2" {
				t.Fatalf("successful state=%#v", got)
			}
		})
	}
}

func TestReadPreservesConfiguredDiffForFailedRetryButImportsCanonicalState(t *testing.T) {
	remote := testAccount("internal-account", "Active", "Failed")
	remote.Name, remote.TechnologyID = "Updated", "technology-2"
	api := &accountStub{account: remote}
	r := &projectAccountResource{client: api, waiter: &instantWaiter{}, projectsAPIURL: "https://projects.example"}
	prior := validModel()
	prior.ID = types.StringValue("internal-account")
	prior.Name = types.StringValue("Build")
	state := stateFor(t, prior)
	response := resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &response)
	var got model
	if d := response.State.Get(context.Background(), &got); d.HasError() {
		t.Fatal(d)
	}
	if got.Name.ValueString() != "Build" || got.TechnologyID.ValueString() != "technology-1" || got.Status.ValueString() != "Active" {
		t.Fatalf("retry read state=%#v", got)
	}

	imported := model{ID: types.StringValue("internal-account"), ProjectID: types.StringValue("project-1"), Timeouts: types.ObjectNull(timeoutAttrTypes)}
	importState := stateFor(t, imported)
	importResponse := resource.ReadResponse{State: importState}
	r.Read(context.Background(), resource.ReadRequest{State: importState}, &importResponse)
	var canonical model
	if d := importResponse.State.Get(context.Background(), &canonical); d.HasError() {
		t.Fatal(d)
	}
	if canonical.Name.ValueString() != "Updated" || canonical.TechnologyID.ValueString() != "technology-2" {
		t.Fatalf("imported canonical state=%#v", canonical)
	}
}

func TestCreateTimeoutRetainsAcceptedIdentity(t *testing.T) {
	api := &accountStub{createAction: vewaccounts.ActionResult{ID: "internal-account"}, account: testAccount("internal-account", "OnBoarding", "")}
	waiter := &instantWaiter{err: &vew.TimeoutError{LastStatus: "OnBoarding"}}
	r := &projectAccountResource{client: api, waiter: waiter, projectsAPIURL: "https://projects.example"}
	m := validModel()
	state := stateFor(t, m)
	response := resource.CreateResponse{State: state, Private: nil}
	r.Create(context.Background(), resource.CreateRequest{Plan: tfsdk.Plan{Schema: state.Schema, Raw: state.Raw}}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected timeout diagnostic")
	}
	var got model
	if d := response.State.Get(context.Background(), &got); d.HasError() {
		t.Fatal(d)
	}
	if got.ID.ValueString() != "internal-account" || got.Status.ValueString() != "OnBoarding" {
		t.Fatalf("timeout did not retain accepted identity: %#v", got)
	}
}

func TestSetStateRedactsArbitraryOnboardingError(t *testing.T) {
	m := validModel()
	m.ID = types.StringValue("internal-account")
	if err := setState(context.Background(), &m, testAccount("internal-account", "Active", "Failed")); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(m.LastOnboardingError.ValueString(), "secret") || !strings.Contains(m.LastOnboardingError.ValueString(), "failed") {
		t.Fatalf("sanitized error=%q", m.LastOnboardingError.ValueString())
	}
}

func validUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, c := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}
