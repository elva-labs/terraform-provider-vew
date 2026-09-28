package technologies

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	vewtechnologies "github.com/elva-labs/terraform-provider-vew/internal/vew/technologies"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type technologyAPIStub struct {
	createCalls int
	updateCalls int
	deleteCalls int
	input       vewtechnologies.TechnologyInput
	remote      vewtechnologies.Technology
	readErr     error
	deleteErr   error
}

func (f *technologyAPIStub) CreateTechnology(_ context.Context, _ string, input vewtechnologies.TechnologyInput, _ string) (string, error) {
	f.createCalls++
	f.input = input
	return "technology-1", nil
}
func (f *technologyAPIStub) GetTechnology(context.Context, string, string) (vewtechnologies.Technology, error) {
	return f.remote, f.readErr
}
func (f *technologyAPIStub) UpdateTechnology(_ context.Context, _, _ string, input vewtechnologies.TechnologyInput) error {
	f.updateCalls++
	f.input = input
	f.remote.Name, f.remote.Description = input.Name, input.Description
	return nil
}
func (f *technologyAPIStub) DeleteTechnology(context.Context, string, string) error {
	f.deleteCalls++
	return f.deleteErr
}

func TestTechnologySchemaAndImportID(t *testing.T) {
	var schemaResponse resource.SchemaResponse
	NewTechnologyResource().Schema(context.Background(), resource.SchemaRequest{}, &schemaResponse)
	attributes := schemaResponse.Schema.Attributes
	for _, name := range []string{"project_id", "name", "description", "id", "created_at", "updated_at"} {
		if _, ok := attributes[name]; !ok {
			t.Errorf("schema is missing %q", name)
		}
	}
	if !attributes["project_id"].IsRequired() || !attributes["name"].IsRequired() || !attributes["description"].IsOptional() || !attributes["id"].IsComputed() {
		t.Fatalf("unexpected technology schema required/optional/computed flags")
	}

	projectID, technologyID, err := parseTechnologyImportID("project-1/technology-1")
	if err != nil || projectID != "project-1" || technologyID != "technology-1" {
		t.Fatalf("parseTechnologyImportID() = %q, %q, %v", projectID, technologyID, err)
	}
	for _, invalid := range []string{"", "project", "/technology", "project/", "a/b/c", " project/technology", "project/technology "} {
		if _, _, err := parseTechnologyImportID(invalid); err == nil {
			t.Errorf("parseTechnologyImportID(%q) unexpectedly succeeded", invalid)
		}
	}
}

func TestTechnologyCreateKeyIsRFC4122Version4(t *testing.T) {
	key, err := newTechnologyIdempotencyKey()
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).Match(key) {
		t.Fatalf("generated invalid UUID %q", key)
	}
}

func TestTechnologyResourceCRUDAndSafeDeleteConflict(t *testing.T) {
	ctx := context.Background()
	client := &technologyAPIStub{remote: vewtechnologies.Technology{
		ID: "technology-1", ProjectID: "project-1", Name: "canonical", Description: "remote description",
		CreatedAt: "created", UpdatedAt: "updated",
	}}
	r := &technologyResource{client: client}
	planned := technologyModel{ProjectID: types.StringValue("project-1"), Name: types.StringValue("requested"), Description: types.StringValue("requested description")}
	planState := technologyTestState(t, planned)
	create := resource.CreateResponse{State: tfsdk.State{Schema: planState.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan{Schema: planState.Schema, Raw: planState.Raw}}, &create)
	if create.Diagnostics.HasError() || client.createCalls != 1 || client.input.Name != "requested" {
		t.Fatalf("create failed: diagnostics=%v calls=%d input=%#v", create.Diagnostics, client.createCalls, client.input)
	}
	var created technologyModel
	if d := create.State.Get(ctx, &created); d.HasError() || created.ID.ValueString() != "technology-1" || created.Name.ValueString() != "canonical" {
		t.Fatalf("created state/diagnostics = %#v/%v", created, d)
	}

	read := resource.ReadResponse{State: create.State}
	r.Read(ctx, resource.ReadRequest{State: create.State}, &read)
	if read.Diagnostics.HasError() || read.State.Raw.IsNull() {
		t.Fatalf("read diagnostics/state = %v/%v", read.Diagnostics, read.State.Raw)
	}

	planned.Name, planned.Description = types.StringValue("renamed"), types.StringValue("new description")
	updatePlan := technologyTestState(t, planned)
	update := resource.UpdateResponse{State: read.State}
	r.Update(ctx, resource.UpdateRequest{
		Plan: tfsdk.Plan{Schema: updatePlan.Schema, Raw: updatePlan.Raw}, State: read.State,
	}, &update)
	var updated technologyModel
	if d := update.State.Get(ctx, &updated); update.Diagnostics.HasError() || d.HasError() || client.updateCalls != 1 || updated.ID.ValueString() != "technology-1" || updated.Name.ValueString() != "renamed" {
		t.Fatalf("update state/diagnostics/calls = %#v/%v/%d", updated, update.Diagnostics, client.updateCalls)
	}

	client.deleteErr = &vew.APIError{Status: 409, Problem: vew.Problem{Code: "TECHNOLOGY_IN_USE", Detail: "secret account parameters"}}
	deletion := resource.DeleteResponse{State: update.State}
	r.Delete(ctx, resource.DeleteRequest{State: update.State}, &deletion)
	if !deletion.Diagnostics.HasError() || deletion.State.Raw.IsNull() || strings.Contains(deletion.Diagnostics[0].Detail(), "secret") {
		t.Fatalf("delete conflict should be safe and retain state: %v", deletion.Diagnostics)
	}
}

func TestTechnologyReadRemovesMissingResourceAndRejectsMismatchedIdentity(t *testing.T) {
	ctx := context.Background()
	model := technologyModel{ID: types.StringValue("technology-1"), ProjectID: types.StringValue("project-1"), Name: types.StringValue("configured")}
	state := technologyTestState(t, model)

	missing := &technologyResource{client: &technologyAPIStub{readErr: &vew.APIError{Status: 404}}}
	missingResponse := resource.ReadResponse{State: state}
	missing.Read(ctx, resource.ReadRequest{State: state}, &missingResponse)
	if missingResponse.Diagnostics.HasError() || !missingResponse.State.Raw.IsNull() {
		t.Fatalf("404 should remove state: diagnostics=%v state=%v", missingResponse.Diagnostics, missingResponse.State.Raw)
	}

	mismatch := &technologyResource{client: &technologyAPIStub{remote: vewtechnologies.Technology{ID: "other", ProjectID: "project-1", Name: "unrelated"}}}
	mismatchResponse := resource.ReadResponse{State: state}
	mismatch.Read(ctx, resource.ReadRequest{State: state}, &mismatchResponse)
	if !mismatchResponse.Diagnostics.HasError() || mismatchResponse.State.Raw.IsNull() {
		t.Fatalf("mismatched identity should diagnose and preserve state: diagnostics=%v state=%v", mismatchResponse.Diagnostics, mismatchResponse.State.Raw)
	}
}

func TestTechnologyPermissionDiagnosticNamesScopeWithoutResponseDetail(t *testing.T) {
	var diagnostics diag.Diagnostics
	addTechnologyError(&diagnostics, "delete", &vew.APIError{
		Status:  403,
		Problem: vew.Problem{Detail: "bearer secret and arbitrary response"},
	}, "project-1")
	if !diagnostics.HasError() {
		t.Fatal("expected permission diagnostic")
	}
	detail := diagnostics[0].Detail()
	if !strings.Contains(detail, "technology.write") || !strings.Contains(detail, "assignment") || strings.Contains(detail, "secret") || strings.Contains(detail, "arbitrary response") {
		t.Fatalf("unsafe or incomplete permission diagnostic: %q", detail)
	}
}

func technologyTestState(t *testing.T, model technologyModel) tfsdk.State {
	t.Helper()
	var schemaResponse resource.SchemaResponse
	NewTechnologyResource().Schema(context.Background(), resource.SchemaRequest{}, &schemaResponse)
	state := tfsdk.State{Schema: schemaResponse.Schema}
	if d := state.Set(context.Background(), &model); d.HasError() {
		t.Fatalf("encode technology state: %v", d)
	}
	return state
}
