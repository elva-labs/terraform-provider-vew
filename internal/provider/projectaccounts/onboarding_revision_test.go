package projectaccounts

import (
	"context"
	"testing"

	vewaccounts "github.com/elva-labs/terraform-provider-vew/internal/vew/projectaccounts"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func activeModel(revision types.String) model {
	m := validModel()
	m.ID = types.StringValue("internal-account")
	m.Status = types.StringValue("Active")
	m.LastOnboardingResult = types.StringValue("Succeeded")
	m.OnboardingRevision = revision
	return m
}

func accountWithRevision(status, revision string) vewaccounts.Account {
	a := testAccount("internal-account", status, "Succeeded")
	a.OnboardingRevision = revision
	return a
}

func runUpdate(t *testing.T, api *accountStub, prior, plan model) model {
	t.Helper()
	r := &projectAccountResource{client: api, waiter: &instantWaiter{}, projectsAPIURL: "https://projects.example"}
	priorState, planState := stateFor(t, prior), stateFor(t, plan)
	response := resource.UpdateResponse{State: priorState}
	r.Update(context.Background(), resource.UpdateRequest{State: priorState, Plan: tfsdk.Plan{Schema: planState.Schema, Raw: planState.Raw}}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("update diagnostics: %v", response.Diagnostics)
	}
	var got model
	if d := response.State.Get(context.Background(), &got); d.HasError() {
		t.Fatal(d)
	}
	return got
}

func TestNewOnboardingRevisionReOnboardsUnchangedConfiguration(t *testing.T) {
	api := &accountStub{reads: []vewaccounts.Account{accountWithRevision("Active", "r1"), accountWithRevision("Active", "r2")}}
	got := runUpdate(t, api, activeModel(types.StringValue("r1")), activeModel(types.StringValue("r2")))
	if api.updateCalls != 1 || api.updateInput.OnboardingRevision != "r2" {
		t.Fatalf("expected one update carrying the new revision, got calls=%d input=%#v", api.updateCalls, api.updateInput)
	}
	if got.OnboardingRevision.ValueString() != "r2" {
		t.Fatalf("state revision=%v", got.OnboardingRevision)
	}
}

func TestSameOnboardingRevisionSendsNothing(t *testing.T) {
	api := &accountStub{account: accountWithRevision("Active", "r1")}
	runUpdate(t, api, activeModel(types.StringValue("r1")), activeModel(types.StringValue("r1")))
	if api.updateCalls != 0 {
		t.Fatalf("unchanged revision must not update, calls=%d", api.updateCalls)
	}
}

func TestRemovingOnboardingRevisionDoesNotReOnboard(t *testing.T) {
	// VEW keeps its stored revision when none is sent, so there is nothing to apply.
	api := &accountStub{account: accountWithRevision("Active", "r1")}
	got := runUpdate(t, api, activeModel(types.StringValue("r1")), activeModel(types.StringNull()))
	if api.updateCalls != 0 {
		t.Fatalf("removing the revision must not update, calls=%d", api.updateCalls)
	}
	if !got.OnboardingRevision.IsNull() {
		t.Fatalf("state revision=%v, want null", got.OnboardingRevision)
	}
}

func TestReadIgnoresRevisionThatIsNotManaged(t *testing.T) {
	// An account whose revision is set elsewhere (or kept after removal from config) must not show
	// a perpetual diff for configurations that never set onboarding_revision.
	api := &accountStub{account: accountWithRevision("Active", "set-elsewhere")}
	r := &projectAccountResource{client: api, waiter: &instantWaiter{}, projectsAPIURL: "https://projects.example"}
	state := stateFor(t, activeModel(types.StringNull()))
	read := resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &read)
	var got model
	if d := read.State.Get(context.Background(), &got); d.HasError() {
		t.Fatal(d)
	}
	if !got.OnboardingRevision.IsNull() {
		t.Fatalf("unmanaged revision leaked into state: %v", got.OnboardingRevision)
	}

	api.account = accountWithRevision("Active", "r3")
	state = stateFor(t, activeModel(types.StringValue("r1")))
	read = resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &read)
	if d := read.State.Get(context.Background(), &got); d.HasError() {
		t.Fatal(d)
	}
	if got.OnboardingRevision.ValueString() != "r3" {
		t.Fatalf("managed revision drift not detected: %v", got.OnboardingRevision)
	}
}

func TestCreateSendsOnboardingRevisionOnlyWhenSet(t *testing.T) {
	for _, tc := range []struct {
		name     string
		revision types.String
		want     string
	}{{"set", types.StringValue("initial"), "initial"}, {"unset", types.StringNull(), ""}} {
		t.Run(tc.name, func(t *testing.T) {
			api := &accountStub{createAction: vewaccounts.ActionResult{ID: "internal-account"}, account: accountWithRevision("Active", tc.want)}
			r := &projectAccountResource{client: api, waiter: &instantWaiter{}, projectsAPIURL: "https://projects.example"}
			m := validModel()
			m.OnboardingRevision = tc.revision
			state := stateFor(t, m)
			resp := resource.CreateResponse{State: state}
			r.Create(context.Background(), resource.CreateRequest{Plan: tfsdk.Plan{Schema: state.Schema, Raw: state.Raw}}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatal(resp.Diagnostics)
			}
			if api.createInput.OnboardingRevision != tc.want {
				t.Fatalf("create revision=%q want %q", api.createInput.OnboardingRevision, tc.want)
			}
		})
	}
}

func TestValidateModelRejectsInvalidOnboardingRevision(t *testing.T) {
	for _, bad := range []string{"", "has space", "slash/es", string(make([]byte, 129))} {
		m := validModel()
		m.OnboardingRevision = types.StringValue(bad)
		if d := validateModel(context.Background(), m); !d.HasError() {
			t.Fatalf("expected a diagnostic for %q", bad)
		}
	}
	for _, good := range []string{"1", "spoke-stacks-2026-10-01", "git:4dae72b", "v1.2.3_rc"} {
		m := validModel()
		m.OnboardingRevision = types.StringValue(good)
		if d := validateModel(context.Background(), m); d.HasError() {
			t.Fatalf("valid revision %q: %v", good, d)
		}
	}
}
