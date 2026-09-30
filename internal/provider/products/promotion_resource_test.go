package products

import (
	"context"
	"strings"
	"testing"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	vewproducts "github.com/elva-labs/terraform-provider-vew/internal/vew/products"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

type promotionAPIStub struct {
	statuses           []string
	promoteCalls       int
	forgetCalls        int
	promoteErr, getErr error
	versionName        string
	versions           []vewproducts.ProductVersion
}

func (f *promotionAPIStub) promotion(status string) vewproducts.Promotion {
	return vewproducts.Promotion{
		ProjectID: "project-1", ProductID: "prod-1", VersionID: "vers-1", VersionName: f.versionName, Stage: "PROD", Status: status,
		Distributions: []vewproducts.Distribution{{AWSAccountID: "123456789012", Region: "eu-north-1", Status: status}},
	}
}

func (f *promotionAPIStub) GetPromotion(context.Context, string, string, string, string) (vewproducts.Promotion, error) {
	if f.getErr != nil {
		return vewproducts.Promotion{}, f.getErr
	}
	return f.promotion(f.statuses[len(f.statuses)-1]), nil
}

func (f *promotionAPIStub) PromoteVersion(context.Context, string, string, string, string) (vewproducts.Promotion, error) {
	if f.promoteErr != nil {
		return vewproducts.Promotion{}, f.promoteErr
	}
	status := f.statuses[min(f.promoteCalls, len(f.statuses)-1)]
	f.promoteCalls++
	return f.promotion(status), nil
}

func (f *promotionAPIStub) ForgetPromotion(context.Context, string, string, string, string) error {
	f.forgetCalls++
	return nil
}

func (f *promotionAPIStub) ListProductVersions(context.Context, string, string) ([]vewproducts.ProductVersion, error) {
	return f.versions, nil
}

func plannedPromotion() promotionModel {
	return promotionModel{
		ID: types.StringUnknown(), ProjectID: types.StringValue("project-1"), ProductID: types.StringValue("prod-1"),
		VersionID: types.StringValue("vers-1"), Stage: types.StringValue("PROD"), VersionName: types.StringUnknown(),
		Status: types.StringUnknown(), Distributions: types.ListUnknown(distributionObjectType), Timeouts: types.ObjectNull(promotionTimeoutTypes),
	}
}

func promotionTestState(t *testing.T, model promotionModel) tfsdk.State {
	t.Helper()
	var schemaResponse resource.SchemaResponse
	NewProductVersionPromotionResource().Schema(context.Background(), resource.SchemaRequest{}, &schemaResponse)
	state := tfsdk.State{Schema: schemaResponse.Schema}
	if d := state.Set(context.Background(), &model); d.HasError() {
		t.Fatalf("encode promotion state: %v", d)
	}
	return state
}

func createPromotion(t *testing.T, client *promotionAPIStub) resource.CreateResponse {
	t.Helper()
	plan := promotionTestState(t, plannedPromotion())
	response := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema}}
	(&promotionResource{client: client, waiter: immediateWaiter{}}).Create(context.Background(), resource.CreateRequest{Plan: tfsdk.Plan{Schema: plan.Schema, Raw: plan.Raw}}, &response)
	return response
}

func TestPromotionCreateRepeatsPutUntilCreated(t *testing.T) {
	client := &promotionAPIStub{statuses: []string{"CREATING", "CREATING", "CREATED"}, versionName: "1.0.0"}
	create := createPromotion(t, client)
	var state promotionModel
	if d := create.State.Get(context.Background(), &state); create.Diagnostics.HasError() || d.HasError() {
		t.Fatalf("diagnostics = %v / %v", create.Diagnostics, d)
	}
	if client.promoteCalls != 3 || state.Status.ValueString() != "CREATED" || state.VersionName.ValueString() != "1.0.0" || state.ID.ValueString() != "project-1/prod-1/vers-1/PROD" || len(state.Distributions.Elements()) != 1 {
		t.Fatalf("state = %#v, calls = %d", state, client.promoteCalls)
	}
}

func TestPromotionCreateFailsOnFailedDistributionButKeepsState(t *testing.T) {
	client := &promotionAPIStub{statuses: []string{"CREATING", "FAILED"}}
	create := createPromotion(t, client)
	if !create.Diagnostics.HasError() || !strings.Contains(create.Diagnostics[0].Detail(), "FAILED") || create.State.Raw.IsNull() {
		t.Fatalf("diagnostics = %v, state null = %v", create.Diagnostics, create.State.Raw.IsNull())
	}
}

func TestPromotionCreateExplainsDomainRejection(t *testing.T) {
	client := &promotionAPIStub{promoteErr: &vew.APIError{Status: 422, Problem: vew.Problem{Code: "DOMAIN_VALIDATION_FAILED", Detail: "secret"}}}
	create := createPromotion(t, client)
	if !create.Diagnostics.HasError() || !strings.Contains(create.Diagnostics[0].Detail(), "release candidates") || strings.Contains(create.Diagnostics[0].Detail(), "secret") || !create.State.Raw.IsNull() {
		t.Fatalf("diagnostics = %v", create.Diagnostics)
	}
}

func TestPromotionReadRemovesMissingAndDeleteForgets(t *testing.T) {
	client := &promotionAPIStub{statuses: []string{"CREATED"}}
	r := &promotionResource{client: client, waiter: immediateWaiter{}}
	create := createPromotion(t, client)
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

func TestPromotionImportAndStageValidation(t *testing.T) {
	var schemaResponse resource.SchemaResponse
	r := &promotionResource{}
	r.Schema(context.Background(), resource.SchemaRequest{}, &schemaResponse)
	for _, tc := range []struct {
		id    string
		valid bool
	}{
		{"project-1/prod-1/vers-1/PROD", true},
		{"project-1/prod-1/vers-1/prod", false},
		{"project-1/prod-1/vers-1", false},
		{"project-1//vers-1/DEV", false},
	} {
		response := resource.ImportStateResponse{State: tfsdk.State{Schema: schemaResponse.Schema, Raw: tftypes.NewValue(schemaResponse.Schema.Type().TerraformType(context.Background()), nil)}}
		r.ImportState(context.Background(), resource.ImportStateRequest{ID: tc.id}, &response)
		if response.Diagnostics.HasError() == tc.valid {
			t.Errorf("import %q diagnostics = %v", tc.id, response.Diagnostics)
		}
	}
}

func TestProductVersionsDataSource(t *testing.T) {
	client := &promotionAPIStub{versions: []vewproducts.ProductVersion{
		{ID: "vers-1", Name: "1.0.0", Type: "RELEASED", Stages: []vewproducts.VersionStage{{Stage: "DEV", Status: "CREATED"}, {Stage: "PROD", Status: "CREATED"}}},
		{ID: "vers-2", Name: "1.1.0-rc.1", Type: "RELEASE_CANDIDATE", Stages: []vewproducts.VersionStage{{Stage: "DEV", Status: "CREATING"}}},
	}}
	d := &versionsDataSource{client: client}
	var schemaResponse datasource.SchemaResponse
	d.Schema(context.Background(), datasource.SchemaRequest{}, &schemaResponse)
	objectType := schemaResponse.Schema.Type().TerraformType(context.Background()).(tftypes.Object)
	config := tfsdk.Config{Schema: schemaResponse.Schema, Raw: tftypes.NewValue(objectType, map[string]tftypes.Value{
		"project_id": tftypes.NewValue(tftypes.String, "project-1"),
		"product_id": tftypes.NewValue(tftypes.String, "prod-1"),
		"versions":   tftypes.NewValue(objectType.AttributeTypes["versions"], nil),
	})}
	response := datasource.ReadResponse{State: tfsdk.State{Schema: schemaResponse.Schema}}
	d.Read(context.Background(), datasource.ReadRequest{Config: config}, &response)
	var model versionsDataSourceModel
	if diags := response.State.Get(context.Background(), &model); response.Diagnostics.HasError() || diags.HasError() || len(model.Versions.Elements()) != 2 {
		t.Fatalf("read = %v / %v / %#v", response.Diagnostics, diags, model)
	}
}
