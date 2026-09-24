package pipelines

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	vewpipelines "github.com/elva-labs/terraform-provider-vew/internal/vew/pipelines"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type apiStub struct {
	reads                                 []vewpipelines.Pipeline
	readErr                               error
	createErr                             error
	updateErr                             error
	retireErr                             error
	createInput                           vewpipelines.CreatePipelineInput
	updateInput                           vewpipelines.UpdatePipelineInput
	createCalls, updateCalls, retireCalls int
}

func (s *apiStub) CreatePipeline(_ context.Context, _ string, input vewpipelines.CreatePipelineInput) (vewpipelines.ActionResult, error) {
	s.createCalls++
	s.createInput = input
	return vewpipelines.ActionResult{ID: "pipeline", RetryAfter: 2 * time.Second}, s.createErr
}
func (s *apiStub) GetPipeline(context.Context, string, string) (vewpipelines.Pipeline, error) {
	if s.readErr != nil {
		return vewpipelines.Pipeline{}, s.readErr
	}
	if len(s.reads) == 0 {
		return remotePipeline("CREATED"), nil
	}
	value := s.reads[0]
	if len(s.reads) > 1 {
		s.reads = s.reads[1:]
	}
	return value, nil
}
func (s *apiStub) UpdatePipeline(_ context.Context, _, _ string, input vewpipelines.UpdatePipelineInput) (vewpipelines.ActionResult, error) {
	s.updateCalls++
	s.updateInput = input
	return vewpipelines.ActionResult{ID: "pipeline", RetryAfter: 3 * time.Second}, s.updateErr
}
func (s *apiStub) RetirePipeline(context.Context, string, string) (vewpipelines.ActionResult, error) {
	s.retireCalls++
	return vewpipelines.ActionResult{ID: "pipeline"}, s.retireErr
}
func (s *apiStub) ListPipelines(context.Context, string) ([]vewpipelines.Pipeline, error) {
	return nil, nil
}

type fastWaiter struct {
	delay, timeLimit time.Duration
	limit            int
	cancel           context.CancelFunc
}

func (w *fastWaiter) Until(ctx context.Context, timeout, delay time.Duration, read vew.StatusReader, evaluate vew.StatusEvaluator) error {
	w.delay, w.timeLimit = delay, timeout
	for i := 0; i < 10; i++ {
		result, err := read(ctx)
		if err != nil {
			return err
		}
		if w.cancel != nil {
			w.cancel()
		}
		done, err := evaluate(result.Status)
		if err != nil || done {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if w.limit > 0 && i+1 >= w.limit {
			return &vew.TimeoutError{LastStatus: result.Status}
		}
	}
	return &vew.TimeoutError{}
}

func remotePipeline(status string) vewpipelines.Pipeline {
	return vewpipelines.Pipeline{ProjectID: "project", ID: "pipeline", Name: "name", Description: "description", RecipeID: "recipe", RecipeName: "recipe name", RecipeVersionID: "version", RecipeVersionName: "1.0.0", BuildInstanceTypes: []string{"m5.large", "m5.xlarge"}, Schedule: "0 0 * * ? *", Status: status, CreatedAt: "2026-09-24", CreatedBy: "creator", UpdatedAt: "2026-09-24", UpdatedBy: "updater"}
}

func validModel() pipelineModel {
	return pipelineModel{ID: types.StringValue("pipeline"), ProjectID: types.StringValue("project"), Name: types.StringValue("name"), Description: types.StringValue("description"), RecipeID: types.StringValue("recipe"), RecipeName: types.StringValue("recipe name"), RecipeVersionID: types.StringValue("version"), RecipeVersionName: types.StringValue("1.0.0"), BuildInstanceTypes: types.ListValueMust(types.StringType, []attr.Value{types.StringValue("m5.large"), types.StringValue("m5.xlarge")}), Schedule: types.StringValue("0 0 * * ? *"), ProductID: types.StringNull(), Status: types.StringValue("CREATED"), DistributionConfigARN: types.StringNull(), InfrastructureConfigARN: types.StringNull(), PipelineARN: types.StringNull(), CreatedAt: types.StringValue("2026-09-24"), CreatedBy: types.StringValue("creator"), UpdatedAt: types.StringValue("2026-09-24"), UpdatedBy: types.StringValue("updater"), Timeouts: types.ObjectNull(timeoutTypes)}
}

func fixtureState(t *testing.T, model pipelineModel) tfsdk.State {
	t.Helper()
	var response resource.SchemaResponse
	NewPipelineResource().Schema(context.Background(), resource.SchemaRequest{}, &response)
	state := tfsdk.State{Schema: response.Schema}
	if d := state.Set(context.Background(), &model); d.HasError() {
		t.Fatal(d)
	}
	return state
}
func stateModel(t *testing.T, state tfsdk.State) pipelineModel {
	t.Helper()
	var model pipelineModel
	if d := state.Get(context.Background(), &model); d.HasError() {
		t.Fatal(d)
	}
	return model
}
func createFixture(t *testing.T, r *pipelineResource, model pipelineModel, ctx context.Context) resource.CreateResponse {
	t.Helper()
	state := fixtureState(t, model)
	response := resource.CreateResponse{State: tfsdk.State{Schema: state.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan{Schema: state.Schema, Raw: state.Raw}}, &response)
	return response
}

func TestPipelineSchema(t *testing.T) {
	var response resource.SchemaResponse
	NewPipelineResource().Schema(context.Background(), resource.SchemaRequest{}, &response)
	for _, name := range []string{"project_id", "name", "description", "recipe_id", "recipe_version_id", "schedule"} {
		field, ok := response.Schema.Attributes[name].(schema.StringAttribute)
		if !ok || !field.IsRequired() {
			t.Fatalf("%s: %#v", name, response.Schema.Attributes[name])
		}
	}
	if field, ok := response.Schema.Attributes["product_id"].(schema.StringAttribute); !ok || !field.IsOptional() {
		t.Fatal("product_id must be optional")
	}
	if field, ok := response.Schema.Attributes["build_instance_types"].(schema.ListAttribute); !ok || !field.IsRequired() {
		t.Fatal("build_instance_types must be required list")
	}
	for _, name := range []string{"id", "recipe_name", "recipe_version_name", "status", "distribution_config_arn", "infrastructure_config_arn", "pipeline_arn", "created_at", "created_by", "updated_at", "updated_by"} {
		if !response.Schema.Attributes[name].IsComputed() {
			t.Fatalf("%s must be computed", name)
		}
	}
	if operationTimeout(context.Background(), types.ObjectNull(timeoutTypes), "create") != time.Hour || operationTimeout(context.Background(), types.ObjectNull(timeoutTypes), "update") != time.Hour || operationTimeout(context.Background(), types.ObjectNull(timeoutTypes), "delete") != 30*time.Minute {
		t.Fatal("timeout defaults")
	}
}

func TestPipelineValidate(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*pipelineModel)
		want bool
	}{
		{"valid", func(*pipelineModel) {}, false},
		{"empty name", func(m *pipelineModel) { m.Name = types.StringValue(" ") }, true},
		{"empty list", func(m *pipelineModel) { m.BuildInstanceTypes = types.ListValueMust(types.StringType, nil) }, true},
		{"empty element", func(m *pipelineModel) {
			m.BuildInstanceTypes = types.ListValueMust(types.StringType, []attr.Value{types.StringValue(" ")})
		}, true},
		{"wrapped schedule", func(m *pipelineModel) { m.Schedule = types.StringValue("cron(0 0 * * ? *)") }, true},
		{"short schedule", func(m *pipelineModel) { m.Schedule = types.StringValue("0 0 * * ?") }, true},
		{"bad timeout", func(m *pipelineModel) {
			m.Timeouts = types.ObjectValueMust(timeoutTypes, map[string]attr.Value{"create": types.StringValue("nope"), "update": types.StringNull(), "delete": types.StringNull()})
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := validModel()
			tc.edit(&model)
			state := fixtureState(t, model)
			var response resource.ValidateConfigResponse
			(&pipelineResource{}).ValidateConfig(context.Background(), resource.ValidateConfigRequest{Config: tfsdk.Config{Schema: state.Schema, Raw: state.Raw}}, &response)
			if response.Diagnostics.HasError() != tc.want {
				t.Fatalf("diagnostics: %v", response.Diagnostics)
			}
		})
	}
}

func TestPipelineCreate(t *testing.T) {
	for _, tc := range []struct {
		name       string
		statuses   []string
		limit      int
		wantError  bool
		wantStatus string
	}{
		{"success", []string{"CREATING", "CREATED"}, 0, false, "CREATED"},
		{"failure", []string{"CREATING", "FAILED"}, 0, true, "FAILED"},
		{"timeout", []string{"CREATING"}, 1, true, "CREATING"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := &apiStub{}
			for _, status := range tc.statuses {
				stub.reads = append(stub.reads, remotePipeline(status))
			}
			waiter := &fastWaiter{limit: tc.limit}
			r := &pipelineResource{client: stub, waiter: waiter}
			model := validModel()
			model.ID = types.StringNull()
			model.Status = types.StringNull()
			nullRemoteMetadata(&model)
			response := createFixture(t, r, model, context.Background())
			if response.Diagnostics.HasError() != tc.wantError {
				t.Fatalf("diagnostics: %v", response.Diagnostics)
			}
			got := stateModel(t, response.State)
			if got.ID.ValueString() != "pipeline" || got.Status.ValueString() != tc.wantStatus || waiter.delay != 2*time.Second || waiter.timeLimit != time.Hour || stub.createCalls != 1 {
				t.Fatalf("state/calls/delay: %#v %d %s", got, stub.createCalls, waiter.delay)
			}
		})
	}
}

func TestPipelineCreateCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stub := &apiStub{reads: []vewpipelines.Pipeline{remotePipeline("CREATING")}}
	r := &pipelineResource{client: stub, waiter: &fastWaiter{cancel: cancel}}
	model := validModel()
	model.ID, model.Status = types.StringNull(), types.StringNull()
	nullRemoteMetadata(&model)
	response := createFixture(t, r, model, ctx)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected cancellation diagnostic")
	}
	got := stateModel(t, response.State)
	if got.ID.ValueString() != "pipeline" || got.Status.ValueString() != "CREATING" {
		t.Fatalf("lost accepted identity: %#v", got)
	}
}

func TestPipelineRead(t *testing.T) {
	product := "product"
	arn := "arn"
	remote := remotePipeline("FAILED")
	remote.ProductID = &product
	remote.PipelineARN = &arn
	remote.BuildInstanceTypes = []string{"m5.xlarge", "m5.large"}
	stub := &apiStub{reads: []vewpipelines.Pipeline{remote}}
	r := &pipelineResource{client: stub}
	state := fixtureState(t, validModel())
	response := resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &response)
	if response.Diagnostics.HasError() {
		t.Fatal(response.Diagnostics)
	}
	got := stateModel(t, response.State)
	if got.Status.ValueString() != "FAILED" || got.ProductID.ValueString() != "product" || got.PipelineARN.ValueString() != "arn" || got.BuildInstanceTypes.Elements()[0].(types.String).ValueString() != "m5.xlarge" {
		t.Fatalf("drift not refreshed: %#v", got)
	}
	for _, status := range []string{"RETIRED", "missing"} {
		t.Run(status, func(t *testing.T) {
			stub := &apiStub{}
			if status == "missing" {
				stub.readErr = &vew.APIError{Status: 404}
			} else {
				stub.reads = []vewpipelines.Pipeline{remotePipeline(status)}
			}
			r := &pipelineResource{client: stub}
			state := fixtureState(t, validModel())
			response := resource.ReadResponse{State: state}
			r.Read(context.Background(), resource.ReadRequest{State: state}, &response)
			if !response.State.Raw.IsNull() {
				t.Fatal("expected removed state")
			}
		})
	}
}

func TestPipelineImport(t *testing.T) {
	for _, tc := range []struct {
		id    string
		valid bool
	}{{"project/pipeline", true}, {"project", false}, {"/pipeline", false}, {"project/", false}, {"project/pipeline/extra", false}} {
		t.Run(tc.id, func(t *testing.T) {
			state := fixtureState(t, validModel())
			response := resource.ImportStateResponse{State: state}
			(&pipelineResource{}).ImportState(context.Background(), resource.ImportStateRequest{ID: tc.id}, &response)
			if response.Diagnostics.HasError() == tc.valid {
				t.Fatalf("diagnostics: %v", response.Diagnostics)
			}
			if tc.valid {
				var project, id types.String
				response.State.GetAttribute(context.Background(), path.Root("project_id"), &project)
				response.State.GetAttribute(context.Background(), path.Root("id"), &id)
				if project.ValueString() != "project" || id.ValueString() != "pipeline" {
					t.Fatal("wrong imported identity")
				}
			}
		})
	}
}

func TestPipelineUpdate(t *testing.T) {
	state := validModel()
	plan := state
	plan.RecipeVersionID = types.StringValue("version-2")
	plan.ProductID = types.StringNull()
	remoteBefore := remotePipeline("CREATED")
	product := "old-product"
	remoteBefore.ProductID = &product
	remoteAfter := remotePipeline("CREATED")
	remoteAfter.RecipeVersionID = "version-2"
	stub := &apiStub{reads: []vewpipelines.Pipeline{remoteBefore, remotePipeline("UPDATING"), remoteAfter}}
	r := &pipelineResource{client: stub, waiter: &fastWaiter{}}
	prior := fixtureState(t, state)
	planned := fixtureState(t, plan)
	response := resource.UpdateResponse{State: prior}
	r.Update(context.Background(), resource.UpdateRequest{State: prior, Plan: tfsdk.Plan{Schema: planned.Schema, Raw: planned.Raw}}, &response)
	if response.Diagnostics.HasError() {
		t.Fatal(response.Diagnostics)
	}
	got := stateModel(t, response.State)
	if got.RecipeVersionID.ValueString() != "version-2" || !got.ProductID.IsNull() || stub.updateCalls != 1 || stub.updateInput.ProductID != nil {
		t.Fatalf("update state/input: %#v %#v", got, stub.updateInput)
	}
	// A pending prior operation must settle before another mutation is considered.
	stub = &apiStub{reads: []vewpipelines.Pipeline{remotePipeline("UPDATING"), remoteAfter}}
	r = &pipelineResource{client: stub, waiter: &fastWaiter{}}
	response = resource.UpdateResponse{State: prior}
	r.Update(context.Background(), resource.UpdateRequest{State: prior, Plan: tfsdk.Plan{Schema: planned.Schema, Raw: planned.Raw}}, &response)
	if response.Diagnostics.HasError() || stub.updateCalls != 0 {
		t.Fatalf("pending reconciliation: %v, calls=%d", response.Diagnostics, stub.updateCalls)
	}
	// A rejected update must retain prior mutable state.
	stub = &apiStub{reads: []vewpipelines.Pipeline{remoteBefore}, updateErr: errors.New("secret")}
	r = &pipelineResource{client: stub, waiter: &fastWaiter{}}
	response = resource.UpdateResponse{State: prior}
	r.Update(context.Background(), resource.UpdateRequest{State: prior, Plan: tfsdk.Plan{Schema: planned.Schema, Raw: planned.Raw}}, &response)
	if !response.Diagnostics.HasError() || stateModel(t, response.State).RecipeVersionID.ValueString() != "version" {
		t.Fatalf("failed update state: %v", response.Diagnostics)
	}
}

func TestPipelineUpdateFailedStatusRecovery(t *testing.T) {
	state := validModel()
	plan := state
	plan.RecipeVersionID = types.StringValue("version-2")
	failed := remotePipeline("FAILED")
	created := remotePipeline("CREATED")
	created.RecipeVersionID = "version-2"
	stub := &apiStub{reads: []vewpipelines.Pipeline{failed, remotePipeline("UPDATING"), created}}
	r := &pipelineResource{client: stub, waiter: &fastWaiter{}}
	prior, planned := fixtureState(t, state), fixtureState(t, plan)
	response := resource.UpdateResponse{State: prior}
	r.Update(context.Background(), resource.UpdateRequest{State: prior, Plan: tfsdk.Plan{Schema: planned.Schema, Raw: planned.Raw}}, &response)
	if response.Diagnostics.HasError() || stub.updateCalls != 1 || stateModel(t, response.State).RecipeVersionID.ValueString() != "version-2" {
		t.Fatalf("failed pipeline recovery: diagnostics=%v calls=%d", response.Diagnostics, stub.updateCalls)
	}
}

func TestPipelineDelete(t *testing.T) {
	for _, tc := range []struct {
		name      string
		statuses  []string
		readErr   error
		retireErr error
		limit     int
		cancel    bool
		wantErr   bool
		wantCalls int
	}{
		{"created", []string{"CREATED", "RETIRED"}, nil, nil, 0, false, false, 1},
		{"failed", []string{"FAILED", "RETIRED"}, nil, nil, 0, false, false, 1},
		{"absent", nil, &vew.APIError{Status: 404}, nil, 0, false, false, 0},
		{"timeout", []string{"CREATED", "CREATED"}, nil, nil, 1, false, true, 1},
		{"cancelled", []string{"CREATED", "CREATED"}, nil, nil, 0, true, true, 1},
		{"terminal failure", []string{"CREATED", "FAILED"}, nil, nil, 0, false, true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := &apiStub{readErr: tc.readErr, retireErr: tc.retireErr}
			for _, status := range tc.statuses {
				stub.reads = append(stub.reads, remotePipeline(status))
			}
			waiter := &fastWaiter{limit: tc.limit}
			ctx := context.Background()
			if tc.cancel {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				defer cancel()
				waiter.cancel = cancel
			}
			r := &pipelineResource{client: stub, waiter: waiter}
			state := fixtureState(t, validModel())
			response := resource.DeleteResponse{State: state}
			r.Delete(ctx, resource.DeleteRequest{State: state}, &response)
			if response.Diagnostics.HasError() != tc.wantErr || stub.retireCalls != tc.wantCalls {
				t.Fatalf("delete: %v, calls=%d", response.Diagnostics, stub.retireCalls)
			}
			if tc.wantErr && stateModel(t, response.State).ID.ValueString() != "pipeline" {
				t.Fatal("lost identity")
			}
		})
	}
}

func TestPipelineUnreleasedDiagnostic(t *testing.T) {
	for _, tc := range []struct {
		name            string
		err             error
		recipe, version string
		target          bool
	}{
		{"exact", &vew.APIError{Status: 422, Problem: vew.Problem{Code: "DOMAIN_VALIDATION_FAILED", Detail: "Version version of recipe recipe has not been released."}}, "recipe", "version", true},
		{"mismatch", &vew.APIError{Status: 422, Problem: vew.Problem{Code: "DOMAIN_VALIDATION_FAILED", Detail: "Version other of recipe recipe has not been released."}}, "recipe", "version", false},
		{"other code", &vew.APIError{Status: 422, Problem: vew.Problem{Code: "OTHER", Detail: "Version version of recipe recipe has not been released."}}, "recipe", "version", false},
		{"unsafe", &vew.APIError{Status: 422, Problem: vew.Problem{Code: "DOMAIN_VALIDATION_FAILED", Detail: "Version version\n of recipe recipe has not been released."}}, "recipe", "version", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, operation := range []string{"create", "update"} {
				var d diag.Diagnostics
				addPipelineError(&d, operation, tc.err, "pipeline", tc.recipe, tc.version)
				if (d[0].Summary() == "Recipe version is not released") != tc.target {
					t.Fatalf("diagnostic: %v", d)
				}
				if tc.target && (!strings.Contains(d[0].Detail(), "terraform apply") || !strings.Contains(d[0].Detail(), "vew_recipe_version_release")) {
					t.Fatalf("missing guidance: %v", d)
				}
			}
		})
	}
}

func TestPipelineUnreleasedCreateAndUpdate(t *testing.T) {
	err := &vew.APIError{Status: 422, Problem: vew.Problem{Code: "DOMAIN_VALIDATION_FAILED", Detail: "Version version of recipe recipe has not been released."}}
	model := validModel()
	model.ID, model.Status = types.StringNull(), types.StringNull()
	nullRemoteMetadata(&model)
	create := createFixture(t, &pipelineResource{client: &apiStub{createErr: err}, waiter: &fastWaiter{}}, model, context.Background())
	if len(create.Diagnostics) == 0 || create.Diagnostics[0].Summary() != "Recipe version is not released" {
		t.Fatalf("create diagnostic: %v", create.Diagnostics)
	}
	state := validModel()
	plan := state
	plan.RecipeVersionID = types.StringValue("version-2")
	plan.Schedule = types.StringValue("0 6 * * ? *")
	updateErr := &vew.APIError{Status: 422, Problem: vew.Problem{Code: "DOMAIN_VALIDATION_FAILED", Detail: "Version version-2 of recipe recipe has not been released."}}
	stub := &apiStub{reads: []vewpipelines.Pipeline{remotePipeline("CREATED")}, updateErr: updateErr}
	prior, planned := fixtureState(t, state), fixtureState(t, plan)
	update := resource.UpdateResponse{State: prior}
	(&pipelineResource{client: stub, waiter: &fastWaiter{}}).Update(context.Background(), resource.UpdateRequest{State: prior, Plan: tfsdk.Plan{Schema: planned.Schema, Raw: planned.Raw}}, &update)
	if len(update.Diagnostics) == 0 || update.Diagnostics[0].Summary() != "Recipe version is not released" {
		t.Fatalf("update diagnostic: %v", update.Diagnostics)
	}
	got := stateModel(t, update.State)
	if got.ID.ValueString() != "pipeline" || got.Schedule.ValueString() != state.Schedule.ValueString() {
		t.Fatalf("update lost prior state: %#v", got)
	}
}

func TestPipelineErrorDiagnosticSanitized(t *testing.T) {
	const bearer = "Bearer super-secret-token"
	const rawBody = `{\"client_secret\":\"super-secret-token\"}`
	for _, tc := range []struct {
		name, operation, resourceID string
		err                         error
		want                        []string
		absent                      []string
	}{
		{
			name: "safe API context", operation: "create", resourceID: "pipeline-123",
			err:    &vew.APIError{Status: 422, Problem: vew.Problem{Code: "DOMAIN_VALIDATION_FAILED", RequestID: "request-123", Detail: bearer + rawBody}},
			want:   []string{"HTTP status 422", "DOMAIN_VALIDATION_FAILED", "request-123", "pipeline-123"},
			absent: []string{bearer, rawBody, "super-secret-token", "client_secret"},
		},
		{
			name: "unsafe API identifiers", operation: "update", resourceID: "pipeline\n" + bearer,
			err:    &vew.APIError{Status: 422, Problem: vew.Problem{Code: "BAD\n" + bearer, RequestID: "request\n" + bearer, Detail: rawBody}},
			want:   []string{"HTTP status 422"},
			absent: []string{bearer, rawBody, "VEW code:", "Request ID:", "Resource ID:"},
		},
		{
			name: "transport error", operation: "create", resourceID: "pipeline-123",
			err:    errors.New("transport failed: " + bearer + rawBody),
			want:   []string{"pipeline create failed", "pipeline-123"},
			absent: []string{bearer, rawBody},
		},
		{
			name: "poll timeout", operation: "update", resourceID: "pipeline-123",
			err:    &vew.TimeoutError{LastStatus: bearer},
			want:   []string{"Timed out", "recoverable state", "pipeline-123"},
			absent: []string{bearer},
		},
		{
			name: "poll cancellation", operation: "delete", resourceID: "pipeline-123",
			err:    context.Canceled,
			want:   []string{"cancelled", "recoverable state", "pipeline-123"},
			absent: []string{bearer},
		},
		{
			name: "unsafe terminal status", operation: "delete", resourceID: "pipeline-123",
			err:    &vew.TerminalStatusError{Status: bearer},
			want:   []string{"pipeline delete failed", "pipeline-123"},
			absent: []string{bearer},
		},
		{
			name: "retirement API body", operation: "delete", resourceID: "pipeline-123",
			err:    &vew.APIError{Status: 503, Problem: vew.Problem{Code: "SERVICE_UNAVAILABLE", RequestID: "request-456", Detail: rawBody}},
			want:   []string{"HTTP status 503", "SERVICE_UNAVAILABLE", "request-456", "pipeline-123"},
			absent: []string{rawBody, "super-secret-token"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var diagnostics diag.Diagnostics
			addPipelineError(&diagnostics, tc.operation, tc.err, tc.resourceID, "recipe", "version")
			if len(diagnostics) != 1 || diagnostics[0].Summary() == "Recipe version is not released" {
				t.Fatalf("expected one generic diagnostic: %v", diagnostics)
			}
			message := diagnostics[0].Summary() + " " + diagnostics[0].Detail()
			for _, value := range tc.want {
				if !strings.Contains(message, value) {
					t.Errorf("diagnostic missing %q: %s", value, message)
				}
			}
			for _, value := range tc.absent {
				if strings.Contains(message, value) {
					t.Errorf("diagnostic leaked %q: %s", value, message)
				}
			}
		})
	}
}
