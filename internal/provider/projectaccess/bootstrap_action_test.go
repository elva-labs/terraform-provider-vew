package projectaccess

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/elva-labs/terraform-provider-vew/internal/providerdata"
	api "github.com/elva-labs/terraform-provider-vew/internal/vew/projectaccess"
	"github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

type bootstrapStub struct {
	result api.ClientAssignment
	err    error
	calls  int
}

func (s *bootstrapStub) AssignClient(context.Context, string, string) (api.ClientAssignment, error) {
	s.calls++
	return s.result, s.err
}
func bootstrapConfig(t *testing.T, project, client any) tfsdk.Config {
	t.Helper()
	var res action.SchemaResponse
	NewBootstrapAction().Schema(context.Background(), action.SchemaRequest{}, &res)
	raw := map[string]tftypes.Value{"project_id": tftypes.NewValue(tftypes.String, project), "client_id": tftypes.NewValue(tftypes.String, client)}
	return tfsdk.Config{Schema: res.Schema, Raw: tftypes.NewValue(res.Schema.Type().TerraformType(context.Background()), raw)}
}
func TestBootstrapActionValidatesTargetAndConfirmation(t *testing.T) {
	for _, tc := range []struct {
		name            string
		project, client any
		result          api.ClientAssignment
		err             error
		want            string
		calls           int
	}{
		{name: "success", project: "project", client: "manager", result: api.ClientAssignment{ProjectID: "project", ClientID: "manager", Status: "ACTIVE"}, calls: 1},
		{name: "self", project: "project", client: "recovery", want: "Self-assignment", calls: 0},
		{name: "unknown", project: tftypes.UnknownValue, client: "manager", want: "Invalid bootstrap target"},
		{name: "blank", project: "project", client: " ", want: "Invalid bootstrap target"},
		{name: "wrong project", project: "project", client: "manager", result: api.ClientAssignment{ProjectID: "other", ClientID: "manager", Status: "ACTIVE"}, want: "Invalid VEW bootstrap response", calls: 1},
		{name: "wrong client", project: "project", client: "manager", result: api.ClientAssignment{ProjectID: "project", ClientID: "other", Status: "ACTIVE"}, want: "Invalid VEW bootstrap response", calls: 1},
		{name: "revoked", project: "project", client: "manager", result: api.ClientAssignment{ProjectID: "project", ClientID: "manager", Status: "REVOKED"}, want: "Invalid VEW bootstrap response", calls: 1},
		{name: "uncertain", project: "project", client: "manager", err: errors.New("secret-and-token"), want: "verify", calls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := &bootstrapStub{result: tc.result, err: tc.err}
			a := &bootstrapAction{client: stub, callerID: "recovery"}
			var res action.InvokeResponse
			a.Invoke(context.Background(), action.InvokeRequest{Config: bootstrapConfig(t, tc.project, tc.client)}, &res)
			if stub.calls != tc.calls {
				t.Fatalf("calls=%d, want %d", stub.calls, tc.calls)
			}
			if tc.want == "" && res.Diagnostics.HasError() {
				t.Fatal(res.Diagnostics)
			}
			if tc.want != "" {
				if !res.Diagnostics.HasError() || !strings.Contains(res.Diagnostics[0].Summary()+res.Diagnostics[0].Detail(), tc.want) {
					t.Fatal(res.Diagnostics)
				}
				if strings.Contains(res.Diagnostics[0].Detail(), "secret-and-token") {
					t.Fatal("diagnostic leaks upstream error")
				}
			}
		})
	}
}
func TestBootstrapRequiresOptInAndNormalResourcesRejectRecovery(t *testing.T) {
	a := &bootstrapAction{}
	var res action.ConfigureResponse
	a.Configure(context.Background(), action.ConfigureRequest{ProviderData: providerdata.Data{ProjectAPIURL: "https://example.test", ClientID: "recovery"}}, &res)
	if !res.Diagnostics.HasError() {
		t.Fatal("missing opt-in accepted")
	}
	r := &accessResource{kind: "client_assignment"}
	var resourceRes resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: providerdata.Data{ProjectAPIURL: "https://example.test", ProjectAccess: &fakeAPI{}, ClientID: "recovery", ProjectClientBootstrap: true}}, &resourceRes)
	if !resourceRes.Diagnostics.HasError() || r.client != nil {
		t.Fatal("normal resource accepted bootstrap mode")
	}
}
func TestClientAssignmentSelfCreateAndUpdateMakeNoWrites(t *testing.T) {
	f := &fakeAPI{}
	r := &accessResource{kind: "client_assignment", client: f, callerID: "manager"}
	s := state(t, r, &clientModel{ID: types.StringUnknown(), ProjectID: types.StringValue("project"), ClientID: types.StringValue("manager"), Status: types.StringValue("ACTIVE")})
	plan := tfsdk.Plan{Schema: s.Schema, Raw: s.Raw}
	create := resource.CreateResponse{State: tfsdk.State{Schema: s.Schema}}
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &create)
	update := resource.UpdateResponse{State: s}
	r.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: s}, &update)
	if !create.Diagnostics.HasError() || !update.Diagnostics.HasError() || f.update != 0 {
		t.Fatalf("self write was accepted: %v %v", create.Diagnostics, update.Diagnostics)
	}
}
