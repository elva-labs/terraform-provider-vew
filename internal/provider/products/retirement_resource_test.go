package products

import (
	"context"
	"strings"
	"testing"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	vewproducts "github.com/elva-labs/terraform-provider-vew/internal/vew/products"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

type retirementAPIStub struct {
	statuses          []string
	retireCalls       int
	forgetCalls       int
	retireErr, getErr error
	lastRequest       vewproducts.RetireRequest
}

func (f *retirementAPIStub) retirement(status string) vewproducts.Retirement {
	return vewproducts.Retirement{
		ProjectID: "project-1", ProductID: "prod-1", VersionID: "vers-1", VersionName: "1.0.3", Status: status,
		Distributions: []vewproducts.Distribution{{AWSAccountID: "123456789012", Region: "eu-north-1", Status: status}},
	}
}

func (f *retirementAPIStub) GetRetirement(context.Context, string, string, string) (vewproducts.Retirement, error) {
	if f.getErr != nil {
		return vewproducts.Retirement{}, f.getErr
	}
	return f.retirement(f.statuses[len(f.statuses)-1]), nil
}

func (f *retirementAPIStub) RetireVersion(_ context.Context, _, _, _ string, request vewproducts.RetireRequest) (vewproducts.Retirement, error) {
	f.lastRequest = request
	if f.retireErr != nil {
		return vewproducts.Retirement{}, f.retireErr
	}
	status := f.statuses[min(f.retireCalls, len(f.statuses)-1)]
	f.retireCalls++
	return f.retirement(status), nil
}

func (f *retirementAPIStub) ForgetRetirement(context.Context, string, string, string) error {
	f.forgetCalls++
	return nil
}

func plannedRetirement(force bool) retirementModel {
	return retirementModel{
		ID: types.StringUnknown(), ProjectID: types.StringValue("project-1"), ProductID: types.StringValue("prod-1"),
		VersionID: types.StringValue("vers-1"), Reason: types.StringValue("Superseded by 1.0.5"), Force: types.BoolValue(force),
		VersionName: types.StringUnknown(), Status: types.StringUnknown(), Distributions: types.ListUnknown(distributionObjectType),
		Timeouts: types.ObjectNull(promotionTimeoutTypes),
	}
}

func retirementTestState(t *testing.T, model retirementModel) tfsdk.State {
	t.Helper()
	var schemaResponse resource.SchemaResponse
	NewProductVersionRetirementResource().Schema(context.Background(), resource.SchemaRequest{}, &schemaResponse)
	state := tfsdk.State{Schema: schemaResponse.Schema}
	if d := state.Set(context.Background(), &model); d.HasError() {
		t.Fatalf("encode retirement state: %v", d)
	}
	return state
}

func createRetirement(t *testing.T, client *retirementAPIStub, force bool) resource.CreateResponse {
	t.Helper()
	plan := retirementTestState(t, plannedRetirement(force))
	response := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema}}
	(&retirementResource{client: client, waiter: immediateWaiter{}}).Create(context.Background(), resource.CreateRequest{Plan: tfsdk.Plan{Schema: plan.Schema, Raw: plan.Raw}}, &response)
	return response
}

func TestRetirementCreateRepeatsPutUntilRetired(t *testing.T) {
	client := &retirementAPIStub{statuses: []string{"RETIRING", "RETIRING", "RETIRED"}}
	create := createRetirement(t, client, true)
	var state retirementModel
	if d := create.State.Get(context.Background(), &state); create.Diagnostics.HasError() || d.HasError() {
		t.Fatalf("diagnostics = %v / %v", create.Diagnostics, d)
	}
	if client.retireCalls != 3 || state.Status.ValueString() != "RETIRED" || state.VersionName.ValueString() != "1.0.3" || state.ID.ValueString() != "project-1/prod-1/vers-1" || len(state.Distributions.Elements()) != 1 {
		t.Fatalf("state = %#v, calls = %d", state, client.retireCalls)
	}
	if client.lastRequest.Reason != "Superseded by 1.0.5" || !client.lastRequest.Force {
		t.Fatalf("request = %#v", client.lastRequest)
	}
}

func TestRetirementCreateExplainsVersionInUse(t *testing.T) {
	client := &retirementAPIStub{retireErr: &vew.APIError{Status: 409, Problem: vew.Problem{Code: "VERSION_IN_USE"}}}
	create := createRetirement(t, client, false)
	if !create.Diagnostics.HasError() || !strings.Contains(create.Diagnostics[0].Detail(), "force = true") || !create.State.Raw.IsNull() {
		t.Fatalf("diagnostics = %v", create.Diagnostics)
	}
}

func TestRetirementCreateExplainsDomainRejection(t *testing.T) {
	client := &retirementAPIStub{retireErr: &vew.APIError{Status: 422, Problem: vew.Problem{Code: "DOMAIN_VALIDATION_FAILED", Detail: "secret"}}}
	create := createRetirement(t, client, false)
	if !create.Diagnostics.HasError() || !strings.Contains(create.Diagnostics[0].Detail(), "latest version") || strings.Contains(create.Diagnostics[0].Detail(), "secret") {
		t.Fatalf("diagnostics = %v", create.Diagnostics)
	}
}

func TestRetirementReadRemovesARestoredVersionAndDeleteForgets(t *testing.T) {
	client := &retirementAPIStub{statuses: []string{"RETIRED"}}
	r := &retirementResource{client: client, waiter: immediateWaiter{}}
	create := createRetirement(t, client, false)
	read := resource.ReadResponse{State: create.State}
	r.Read(context.Background(), resource.ReadRequest{State: create.State}, &read)
	if read.Diagnostics.HasError() || read.State.Raw.IsNull() {
		t.Fatalf("read = %v", read.Diagnostics)
	}
	deletion := resource.DeleteResponse{State: read.State}
	r.Delete(context.Background(), resource.DeleteRequest{State: read.State}, &deletion)
	if deletion.Diagnostics.HasError() || client.forgetCalls != 1 {
		t.Fatalf("delete = %v, calls = %d", deletion.Diagnostics, client.forgetCalls)
	}
	client.getErr = &vew.APIError{Status: 404}
	gone := resource.ReadResponse{State: create.State}
	r.Read(context.Background(), resource.ReadRequest{State: create.State}, &gone)
	if gone.Diagnostics.HasError() || !gone.State.Raw.IsNull() {
		t.Fatalf("expected removal, diagnostics = %v", gone.Diagnostics)
	}
}

func TestRetirementImport(t *testing.T) {
	var schemaResponse resource.SchemaResponse
	r := &retirementResource{}
	r.Schema(context.Background(), resource.SchemaRequest{}, &schemaResponse)
	for _, tc := range []struct {
		id    string
		valid bool
	}{
		{"project-1/prod-1/vers-1", true},
		{"project-1/prod-1/vers-1/PROD", false},
		{"project-1//vers-1", false},
		{"project-1/prod-1", false},
	} {
		response := resource.ImportStateResponse{State: tfsdk.State{Schema: schemaResponse.Schema, Raw: tftypes.NewValue(schemaResponse.Schema.Type().TerraformType(context.Background()), nil)}}
		r.ImportState(context.Background(), resource.ImportStateRequest{ID: tc.id}, &response)
		if response.Diagnostics.HasError() == tc.valid {
			t.Errorf("import %q diagnostics = %v", tc.id, response.Diagnostics)
		}
	}
}
