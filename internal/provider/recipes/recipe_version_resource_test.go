package recipes

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	vewrecipes "github.com/elva-labs/terraform-provider-vew/internal/vew/recipes"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type recipeVersionAPIStub struct {
	remote       vewrecipes.RecipeVersion
	reads        []vewrecipes.RecipeVersion
	err          error
	createAction vewrecipes.ActionResult
	createInput  vewrecipes.CreateRecipeVersionInput
	createCalls  int
	updateAction vewrecipes.ActionResult
	updateInput  vewrecipes.UpdateRecipeVersionInput
	updateCalls  int
	updateErr    error
	retireAction vewrecipes.ActionResult
	retireCalls  int
	retireErr    error
}

func (f *recipeVersionAPIStub) CreateRecipeVersion(_ context.Context, _, _ string, input vewrecipes.CreateRecipeVersionInput) (vewrecipes.ActionResult, error) {
	f.createCalls++
	f.createInput = input
	return f.createAction, f.err
}
func (f *recipeVersionAPIStub) GetRecipeVersion(context.Context, string, string, string) (vewrecipes.RecipeVersion, error) {
	if len(f.reads) != 0 {
		f.remote = f.reads[0]
		if len(f.reads) > 1 {
			f.reads = f.reads[1:]
		}
	}
	return f.remote, f.err
}
func (f *recipeVersionAPIStub) UpdateRecipeVersion(_ context.Context, _, _, _ string, input vewrecipes.UpdateRecipeVersionInput) (vewrecipes.ActionResult, error) {
	f.updateCalls++
	f.updateInput = input
	return f.updateAction, f.updateErr
}

type immediateRecipeWaiter struct {
	timeout, initialDelay time.Duration
	statuses              []string
	limit                 int
	afterRead             func()
}

func (w *immediateRecipeWaiter) Until(ctx context.Context, timeout, delay time.Duration, read vew.StatusReader, evaluate vew.StatusEvaluator) error {
	w.timeout, w.initialDelay = timeout, delay
	for attempt := 0; attempt < 20; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		result, err := read(ctx)
		if err != nil {
			return err
		}
		w.statuses = append(w.statuses, result.Status)
		if w.afterRead != nil {
			w.afterRead()
		}
		if done, err := evaluate(result.Status); done || err != nil {
			return err
		}
		if w.limit > 0 && attempt+1 >= w.limit {
			return &vew.TimeoutError{LastStatus: result.Status}
		}
	}
	return &vew.TimeoutError{}
}
func (f *recipeVersionAPIStub) RetireRecipeVersion(context.Context, string, string, string) (vewrecipes.ActionResult, error) {
	f.retireCalls++
	return f.retireAction, f.retireErr
}

func recipeVersionFixture(status string, configured *[]vewrecipes.ComponentVersion) vewrecipes.RecipeVersion {
	return vewrecipes.RecipeVersion{
		RecipeID: "recipe", ID: "version", Components: configured,
		EffectiveComponents: []vewrecipes.ComponentVersion{
			{ComponentID: "selected", ComponentName: "selected", VersionID: "selected-version", VersionName: "1.0.0", Type: "APP", Order: 1},
			{ComponentID: "mandatory", ComponentName: "mandatory", VersionID: "mandatory-version", VersionName: "1.0.0", Type: "HELPER", Order: 2},
		},
		Description: "remote description", Name: "1.0.0", VolumeSize: "20.0", Integrations: []string{"a", "z"},
		Status: status, CreatedAt: "2026-09-23T00:00:00Z", CreatedBy: "creator",
		UpdatedAt: "2026-09-23T00:00:00Z", UpdatedBy: "updater",
	}
}

func recipeVersionCreatePlan(t *testing.T) recipeVersionModel {
	model := validRecipeVersionModel(t)
	model.ID, model.Name, model.Status = types.StringNull(), types.StringNull(), types.StringNull()
	model.CreatedAt, model.CreatedBy, model.UpdatedAt, model.UpdatedBy = types.StringNull(), types.StringNull(), types.StringNull(), types.StringNull()
	model.EffectiveComponents = types.ListNull(types.ObjectType{AttrTypes: recipeComponentTypes})
	return model
}

func createRecipeVersion(t *testing.T, ctx context.Context, r *recipeVersionResource, model recipeVersionModel) resource.CreateResponse {
	t.Helper()
	state := recipeVersionState(t, model)
	response := resource.CreateResponse{State: tfsdk.State{Schema: state.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan{Schema: state.Schema, Raw: state.Raw}}, &response)
	return response
}

func TestRecipeVersionCreate(t *testing.T) {
	selected := []vewrecipes.ComponentVersion{{ComponentID: "component", ComponentName: "component name", VersionID: "component-version", VersionName: "1.0.0", Type: "APP", Order: 1}}
	for _, tc := range []struct {
		name       string
		statuses   []string
		limit      int
		cancel     bool
		wantError  bool
		wantStatus string
	}{
		{"validated", []string{"CREATING", "CREATED", "TESTING", "UPDATING", "VALIDATED"}, 0, false, false, "VALIDATED"},
		{"failed", []string{"CREATING", "FAILED"}, 0, false, true, "FAILED"},
		{"timeout", []string{"TESTING"}, 1, false, true, "TESTING"},
		{"cancellation", []string{"CREATING"}, 0, true, true, "CREATING"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &recipeVersionAPIStub{createAction: vewrecipes.ActionResult{ID: "version", RetryAfter: 2 * time.Second}}
			for _, status := range tc.statuses {
				client.reads = append(client.reads, recipeVersionFixture(status, &selected))
			}
			waiter := &immediateRecipeWaiter{limit: tc.limit}
			ctx := context.Background()
			if tc.cancel {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				waiter.afterRead = cancel
			}
			r := &recipeVersionResource{client: client, waiter: waiter}
			response := createRecipeVersion(t, ctx, r, recipeVersionCreatePlan(t))
			if response.Diagnostics.HasError() != tc.wantError {
				t.Fatalf("create diagnostics = %v", response.Diagnostics)
			}
			var got recipeVersionModel
			if d := response.State.Get(context.Background(), &got); d.HasError() || got.ID.ValueString() != "version" || got.Status.ValueString() != tc.wantStatus {
				t.Fatalf("saved state/diagnostics = %#v/%v", got, d)
			}
			if client.createCalls != 1 || client.createInput.ReleaseType != "MAJOR" || client.createInput.VolumeSize != "20" || len(client.createInput.Components) != 1 || waiter.timeout != time.Hour || waiter.initialDelay != 2*time.Second {
				t.Fatalf("create call/input/wait = %d/%#v/%s/%s", client.createCalls, client.createInput, waiter.timeout, waiter.initialDelay)
			}
		})
	}
}

func updateRecipeVersion(t *testing.T, r *recipeVersionResource, state, plan recipeVersionModel) resource.UpdateResponse {
	t.Helper()
	prior := recipeVersionState(t, state)
	planned := recipeVersionState(t, plan)
	response := resource.UpdateResponse{State: prior}
	r.Update(context.Background(), resource.UpdateRequest{State: prior, Plan: tfsdk.Plan{Schema: planned.Schema, Raw: planned.Raw}}, &response)
	return response
}

func TestRecipeVersionUpdate(t *testing.T) {
	selected := []vewrecipes.ComponentVersion{{ComponentID: "component", ComponentName: "component name", VersionID: "component-version", VersionName: "1.0.0", Type: "APP", Order: 1}}
	makeRemote := func(status, description string) vewrecipes.RecipeVersion {
		remote := recipeVersionFixture(status, &selected)
		remote.Description = description
		return remote
	}
	t.Run("successful full update", func(t *testing.T) {
		client := &recipeVersionAPIStub{updateAction: vewrecipes.ActionResult{ID: "version", RetryAfter: 3 * time.Second}, reads: []vewrecipes.RecipeVersion{
			makeRemote("VALIDATED", "description"), makeRemote("UPDATING", "new description"), makeRemote("VALIDATED", "new description"),
		}}
		waiter := &immediateRecipeWaiter{}
		r := &recipeVersionResource{client: client, waiter: waiter}
		state := validRecipeVersionModel(t)
		plan := state
		plan.Description = types.StringValue("new description")
		response := updateRecipeVersion(t, r, state, plan)
		if response.Diagnostics.HasError() || client.updateCalls != 1 || client.updateInput.Description != "new description" || client.updateInput.VolumeSize != "20" || len(client.updateInput.Components) != 1 || waiter.initialDelay != 3*time.Second {
			t.Fatalf("update diagnostics/calls/input/wait = %v/%d/%#v/%s", response.Diagnostics, client.updateCalls, client.updateInput, waiter.initialDelay)
		}
		var got recipeVersionModel
		if d := response.State.Get(context.Background(), &got); d.HasError() || got.Description.ValueString() != "new description" || got.Status.ValueString() != "VALIDATED" {
			t.Fatalf("updated state/diagnostics = %#v/%v", got, d)
		}
	})
	t.Run("failed validation preserves prior mutable state for retry", func(t *testing.T) {
		client := &recipeVersionAPIStub{updateAction: vewrecipes.ActionResult{ID: "version"}, reads: []vewrecipes.RecipeVersion{
			makeRemote("VALIDATED", "description"), makeRemote("FAILED", "new description"),
		}}
		r := &recipeVersionResource{client: client, waiter: &immediateRecipeWaiter{}}
		state := validRecipeVersionModel(t)
		plan := state
		plan.Description = types.StringValue("new description")
		response := updateRecipeVersion(t, r, state, plan)
		if !response.Diagnostics.HasError() || client.updateCalls != 1 {
			t.Fatalf("failed update diagnostics/calls = %v/%d", response.Diagnostics, client.updateCalls)
		}
		var failed recipeVersionModel
		if d := response.State.Get(context.Background(), &failed); d.HasError() || failed.ID.ValueString() != "version" || failed.Status.ValueString() != "FAILED" || failed.Description.ValueString() != "description" {
			t.Fatalf("failed state/diagnostics = %#v/%v", failed, d)
		}
		client.reads = []vewrecipes.RecipeVersion{makeRemote("FAILED", "new description"), makeRemote("VALIDATED", "new description")}
		response = updateRecipeVersion(t, r, failed, plan)
		if response.Diagnostics.HasError() || client.updateCalls != 2 {
			t.Fatalf("retry diagnostics/calls = %v/%d", response.Diagnostics, client.updateCalls)
		}
	})
	t.Run("pending operation reconciles without duplicate mutation", func(t *testing.T) {
		client := &recipeVersionAPIStub{reads: []vewrecipes.RecipeVersion{
			makeRemote("TESTING", "new description"), makeRemote("VALIDATED", "new description"),
		}}
		r := &recipeVersionResource{client: client, waiter: &immediateRecipeWaiter{}}
		state := validRecipeVersionModel(t)
		plan := state
		plan.Description = types.StringValue("new description")
		response := updateRecipeVersion(t, r, state, plan)
		if response.Diagnostics.HasError() || client.updateCalls != 0 {
			t.Fatalf("pending reconciliation diagnostics/calls = %v/%d", response.Diagnostics, client.updateCalls)
		}
	})
	t.Run("timeout edit is state only", func(t *testing.T) {
		client := &recipeVersionAPIStub{remote: makeRemote("VALIDATED", "description")}
		r := &recipeVersionResource{client: client, waiter: &immediateRecipeWaiter{}}
		state := validRecipeVersionModel(t)
		plan := state
		plan.Timeouts = types.ObjectValueMust(recipeTimeoutTypes, map[string]attr.Value{"create": types.StringNull(), "update": types.StringValue("2h"), "delete": types.StringNull()})
		var plannedComponents []recipeComponentModel
		if diagnostics := plan.ConfiguredComponents.ElementsAs(context.Background(), &plannedComponents, false); diagnostics.HasError() {
			t.Fatalf("decode planned components: %v", diagnostics)
		}
		plannedComponents[0].ComponentName = types.StringUnknown()
		plannedComponents[0].VersionName = types.StringUnknown()
		plannedComponents[0].Order = types.Int64Unknown()
		planned, diagnostics := types.ListValueFrom(context.Background(), types.ObjectType{AttrTypes: recipeComponentTypes}, plannedComponents)
		if diagnostics.HasError() {
			t.Fatalf("encode planned components: %v", diagnostics)
		}
		plan.ConfiguredComponents = planned
		response := updateRecipeVersion(t, r, state, plan)
		if response.Diagnostics.HasError() || client.updateCalls != 0 {
			t.Fatalf("state-only adoption diagnostics/calls = %v/%d", response.Diagnostics, client.updateCalls)
		}
		var got recipeVersionModel
		if d := response.State.Get(context.Background(), &got); d.HasError() || got.ReleaseType.ValueString() != "MAJOR" || got.Timeouts.IsNull() {
			t.Fatalf("state-only result/diagnostics = %#v/%v", got, d)
		}
		var gotComponents []recipeComponentModel
		if d := got.ConfiguredComponents.ElementsAs(context.Background(), &gotComponents, false); d.HasError() || len(gotComponents) != 1 || gotComponents[0].ComponentName.ValueString() != "component name" || gotComponents[0].VersionName.ValueString() != "1.0.0" || gotComponents[0].Order.ValueInt64() != 1 {
			t.Fatalf("state-only components/diagnostics = %#v/%v", gotComponents, d)
		}
	})
}

func TestRecipeVersionDelete(t *testing.T) {
	selected := []vewrecipes.ComponentVersion{{ComponentID: "component", ComponentName: "component name", VersionID: "component-version", VersionName: "1.0.0", Type: "APP", Order: 1}}
	for _, tc := range []struct {
		name       string
		statuses   []string
		limit      int
		wantError  bool
		wantRetire int
		wantStatus string
	}{
		{"validated", []string{"VALIDATED", "VALIDATED", "RETIRED"}, 0, false, 1, ""},
		{"failed version", []string{"FAILED", "RETIRED"}, 0, false, 1, ""},
		{"released version", []string{"RELEASED", "RETIRED"}, 0, false, 1, ""},
		{"already retired", []string{"RETIRED"}, 0, false, 0, ""},
		{"retirement failed", []string{"VALIDATED", "FAILED"}, 0, true, 1, "FAILED"},
		{"retirement timeout", []string{"VALIDATED", "VALIDATED"}, 1, true, 1, "VALIDATED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &recipeVersionAPIStub{retireAction: vewrecipes.ActionResult{ID: "version", RetryAfter: 4 * time.Second}}
			for _, status := range tc.statuses {
				client.reads = append(client.reads, recipeVersionFixture(status, &selected))
			}
			waiter := &immediateRecipeWaiter{limit: tc.limit}
			r := &recipeVersionResource{client: client, waiter: waiter}
			state := recipeVersionState(t, validRecipeVersionModel(t))
			response := resource.DeleteResponse{State: state}
			r.Delete(context.Background(), resource.DeleteRequest{State: state}, &response)
			if response.Diagnostics.HasError() != tc.wantError || client.retireCalls != tc.wantRetire {
				t.Fatalf("delete diagnostics/retire calls = %v/%d", response.Diagnostics, client.retireCalls)
			}
			if tc.wantError {
				var got recipeVersionModel
				if d := response.State.Get(context.Background(), &got); d.HasError() || got.ID.ValueString() != "version" || got.Status.ValueString() != tc.wantStatus {
					t.Fatalf("recoverable delete state/diagnostics = %#v/%v", got, d)
				}
			} else if !response.State.Raw.IsNull() {
				t.Fatalf("successful delete retained state: %v", response.State.Raw)
			}
			if tc.wantRetire == 1 && waiter.initialDelay != 4*time.Second {
				t.Fatalf("retire initial delay = %s", waiter.initialDelay)
			}
			if tc.wantRetire == 1 && waiter.timeout != 30*time.Minute {
				t.Fatalf("retire timeout = %s", waiter.timeout)
			}
		})
	}
	client := &recipeVersionAPIStub{err: &vew.APIError{Status: 404}}
	r := &recipeVersionResource{client: client, waiter: &immediateRecipeWaiter{}}
	state := recipeVersionState(t, validRecipeVersionModel(t))
	response := resource.DeleteResponse{State: state}
	r.Delete(context.Background(), resource.DeleteRequest{State: state}, &response)
	if response.Diagnostics.HasError() || !response.State.Raw.IsNull() || client.retireCalls != 0 {
		t.Fatalf("404 delete result = %v/%v/%d", response.Diagnostics, response.State.Raw, client.retireCalls)
	}
}

func TestRecipeVersionRead(t *testing.T) {
	selected := []vewrecipes.ComponentVersion{{ComponentID: "selected", ComponentName: "selected", VersionID: "selected-version", VersionName: "1.0.0", Type: "APP", Order: 1}}
	empty := []vewrecipes.ComponentVersion{}
	for _, tc := range []struct {
		name   string
		remote vewrecipes.RecipeVersion
		err    error
		absent bool
	}{
		{"current", recipeVersionFixture("VALIDATED", &selected), nil, false},
		{"explicit empty", recipeVersionFixture("VALIDATED", &empty), nil, false},
		{"historical", recipeVersionFixture("VALIDATED", nil), nil, false},
		{"released", recipeVersionFixture("RELEASED", &selected), nil, false},
		{"retired", recipeVersionFixture("RETIRED", &selected), nil, true},
		{"not found", vewrecipes.RecipeVersion{}, &vew.APIError{Status: 404}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := validRecipeVersionModel(t)
			model.ConfiguredComponents = types.ListNull(types.ObjectType{AttrTypes: recipeComponentTypes})
			state := recipeVersionState(t, model)
			r := &recipeVersionResource{client: &recipeVersionAPIStub{remote: tc.remote, err: tc.err}}
			response := resource.ReadResponse{State: state}
			r.Read(context.Background(), resource.ReadRequest{State: state}, &response)
			if response.Diagnostics.HasError() {
				t.Fatalf("read diagnostics = %v", response.Diagnostics)
			}
			if tc.absent {
				if !response.State.Raw.IsNull() {
					t.Fatalf("absent version retained state: %v", response.State.Raw)
				}
				return
			}
			var got recipeVersionModel
			if d := response.State.Get(context.Background(), &got); d.HasError() {
				t.Fatalf("decode read state: %v", d)
			}
			if got.ID.ValueString() != "version" || got.RecipeID.ValueString() != "recipe" || got.VolumeSize.ValueInt64() != 20 || got.Description.ValueString() != "remote description" || got.Status.ValueString() != tc.remote.Status {
				t.Fatalf("unexpected refreshed state: %#v", got)
			}
			configured, d := expandRecipeComponents(context.Background(), got.ConfiguredComponents)
			if d.HasError() || len(configured) != 2 || configured[0].ComponentID != "selected" || configured[1].ComponentID != "mandatory" {
				t.Fatalf("import comparison baseline = %v, %v", configured, d)
			}
			effective, d := expandRecipeComponents(context.Background(), got.EffectiveComponents)
			if d.HasError() || len(effective) != 2 || effective[1].ComponentID != "mandatory" {
				t.Fatalf("effective components = %v, %v", effective, d)
			}
			if got.ReleaseType.ValueString() != "MAJOR" {
				t.Fatalf("read should preserve unobservable release type: %v", got.ReleaseType)
			}
		})
	}
}

func TestRecipeVersionImport(t *testing.T) {
	for _, value := range []string{"", "project/recipe", "/recipe/version", "project//version", "project/recipe/", "project/recipe/version/extra"} {
		if _, _, _, err := parseRecipeVersionImportID(value); err == nil {
			t.Fatalf("invalid import ID %q accepted", value)
		}
	}
	model := validRecipeVersionModel(t)
	state := recipeVersionState(t, model)
	r := &recipeVersionResource{client: &recipeVersionAPIStub{remote: recipeVersionFixture("VALIDATED", nil)}}
	response := resource.ImportStateResponse{State: state}
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: "project/recipe/version"}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("import diagnostics: %v", response.Diagnostics)
	}
	var imported recipeVersionModel
	if d := response.State.Get(context.Background(), &imported); d.HasError() || imported.ProjectID.ValueString() != "project" || imported.RecipeID.ValueString() != "recipe" || imported.ID.ValueString() != "version" || !imported.ReleaseType.IsNull() {
		t.Fatalf("imported identity/diagnostics = %#v/%v", imported, d)
	}
	imported.ConfiguredComponents = types.ListNull(types.ObjectType{AttrTypes: recipeComponentTypes})
	importedState := recipeVersionState(t, imported)
	read := resource.ReadResponse{State: importedState}
	r.Read(context.Background(), resource.ReadRequest{State: importedState}, &read)
	if read.Diagnostics.HasError() {
		t.Fatalf("imported refresh diagnostics: %v", read.Diagnostics)
	}
	var refreshed recipeVersionModel
	if d := read.State.Get(context.Background(), &refreshed); d.HasError() || refreshed.ConfiguredComponents.IsNull() || !refreshed.ReleaseType.IsNull() {
		t.Fatalf("historical imported state/diagnostics = %#v/%v", refreshed, d)
	}
}

func recipePlanResponse(t *testing.T, state, plan recipeVersionModel) resource.ModifyPlanResponse {
	t.Helper()
	prior := recipeVersionState(t, state)
	planned := recipeVersionState(t, plan)
	response := resource.ModifyPlanResponse{Plan: tfsdk.Plan{Schema: planned.Schema, Raw: planned.Raw}}
	(&recipeVersionResource{}).ModifyPlan(context.Background(), resource.ModifyPlanRequest{State: prior, Plan: response.Plan}, &response)
	return response
}

func replacementIncludes(response resource.ModifyPlanResponse, name string) bool {
	for _, item := range response.RequiresReplace {
		if item.String() == name {
			return true
		}
	}
	return false
}

func TestRecipeVersionReleaseTypePlanning(t *testing.T) {
	state := validRecipeVersionModel(t)
	state.ReleaseType = types.StringNull()
	plan := state
	plan.ReleaseType = types.StringValue("PATCH")
	response := recipePlanResponse(t, state, plan)
	if !response.Diagnostics.HasError() {
		t.Fatalf("imported release type must not be guessed: %v", response.Diagnostics)
	}
	state.ReleaseType = types.StringValue("MAJOR")
	response = recipePlanResponse(t, state, plan)
	if response.Diagnostics.HasError() || !replacementIncludes(response, "release_type") {
		t.Fatalf("known release type change must replace: %v, %v", response.Diagnostics, response.RequiresReplace)
	}
	plan.ReleaseType = types.StringNull()
	response = recipePlanResponse(t, state, plan)
	if response.Diagnostics.HasError() || replacementIncludes(response, "release_type") {
		t.Fatalf("forgetting a create instruction must not replace: %v, %v", response.Diagnostics, response.RequiresReplace)
	}
}

func TestRecipeVersionPlan(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status string
		edit   func(*recipeVersionModel)
		want   string
	}{
		{"project change", "VALIDATED", func(m *recipeVersionModel) { m.ProjectID = types.StringValue("other") }, "project_id"},
		{"recipe change", "VALIDATED", func(m *recipeVersionModel) { m.RecipeID = types.StringValue("other") }, "recipe_id"},
		{"draft description", "VALIDATED", func(m *recipeVersionModel) { m.Description = types.StringValue("new") }, ""},
		{"released description", "RELEASED", func(m *recipeVersionModel) { m.Description = types.StringValue("new") }, "description"},
		{"released volume", "RELEASED", func(m *recipeVersionModel) { m.VolumeSize = types.Int64Value(30) }, "volume_size"},
		{"released components", "RELEASED", func(m *recipeVersionModel) { m.ConfiguredComponents = versionComponents(t) }, "configured_components"},
		{"released integrations", "RELEASED", func(m *recipeVersionModel) {
			m.Integrations = types.SetValueMust(types.StringType, []attr.Value{types.StringValue("other")})
		}, "integrations"},
		{"timeout only", "RELEASED", func(m *recipeVersionModel) {
			m.Timeouts = types.ObjectValueMust(recipeTimeoutTypes, map[string]attr.Value{"create": types.StringValue("2h"), "update": types.StringNull(), "delete": types.StringNull()})
		}, ""},
		{"integration order", "RELEASED", func(m *recipeVersionModel) {
			m.Integrations = types.SetValueMust(types.StringType, []attr.Value{types.StringValue("a"), types.StringValue("z")})
		}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := validRecipeVersionModel(t)
			state.Status = types.StringValue(tc.status)
			plan := state
			tc.edit(&plan)
			response := recipePlanResponse(t, state, plan)
			if response.Diagnostics.HasError() {
				t.Fatalf("plan diagnostics: %v", response.Diagnostics)
			}
			if tc.want == "" && len(response.RequiresReplace) != 0 {
				t.Fatalf("unexpected replacement: %v", response.RequiresReplace)
			}
			if tc.want != "" && !replacementIncludes(response, tc.want) {
				t.Fatalf("want replacement %s, got %v", tc.want, response.RequiresReplace)
			}
			if (tc.name == "timeout only" || tc.name == "integration order") && !equivalentRecipeVersionConfiguration(context.Background(), state, plan) {
				t.Fatal("state-only edit should have equivalent VEW payload")
			}
		})
	}
}

func recipeVersionState(t *testing.T, model recipeVersionModel) tfsdk.State {
	t.Helper()
	var response resource.SchemaResponse
	NewRecipeVersionResource().Schema(context.Background(), resource.SchemaRequest{}, &response)
	state := tfsdk.State{Schema: response.Schema}
	if diagnostics := state.Set(context.Background(), &model); diagnostics.HasError() {
		t.Fatalf("encode recipe version state: %v", diagnostics)
	}
	return state
}

func versionComponents(t *testing.T, components ...vewrecipes.ComponentVersion) types.List {
	t.Helper()
	value, diagnostics := recipeComponentList(context.Background(), components)
	if diagnostics.HasError() {
		t.Fatalf("encode recipe components: %v", diagnostics)
	}
	return value
}

func validRecipeVersionModel(t *testing.T) recipeVersionModel {
	t.Helper()
	return recipeVersionModel{
		ID: types.StringValue("version"), ProjectID: types.StringValue("project"), RecipeID: types.StringValue("recipe"),
		Description: types.StringValue("description"), ReleaseType: types.StringValue("MAJOR"), VolumeSize: types.Int64Value(20),
		Integrations:         types.SetValueMust(types.StringType, []attr.Value{types.StringValue("z"), types.StringValue("a")}),
		ConfiguredComponents: versionComponents(t, vewrecipes.ComponentVersion{ComponentID: "component", ComponentName: "component name", VersionID: "component-version", VersionName: "1.0.0", Type: "APP", Order: 1}),
		EffectiveComponents:  versionComponents(t), Name: types.StringValue("1.0.0"), Status: types.StringValue("VALIDATED"),
		CreatedAt: types.StringValue("2026-09-23T00:00:00Z"), CreatedBy: types.StringValue("creator"),
		UpdatedAt: types.StringValue("2026-09-23T00:00:00Z"), UpdatedBy: types.StringValue("updater"),
		Timeouts: types.ObjectNull(recipeTimeoutTypes),
	}
}

func TestRecipeVersionSchema(t *testing.T) {
	var response resource.SchemaResponse
	NewRecipeVersionResource().Schema(context.Background(), resource.SchemaRequest{}, &response)
	for _, name := range []string{"project_id", "recipe_id", "description"} {
		field, ok := response.Schema.Attributes[name].(schema.StringAttribute)
		if !ok || !field.IsRequired() {
			t.Fatalf("%s must be a required string: %#v", name, response.Schema.Attributes[name])
		}
	}
	if field, ok := response.Schema.Attributes["release_type"].(schema.StringAttribute); !ok || !field.IsOptional() {
		t.Fatalf("release_type must be optional for import: %#v", response.Schema.Attributes["release_type"])
	}
	if field, ok := response.Schema.Attributes["volume_size"].(schema.Int64Attribute); !ok || !field.IsRequired() {
		t.Fatalf("volume_size must be required integer: %#v", response.Schema.Attributes["volume_size"])
	}
	if field, ok := response.Schema.Attributes["integrations"].(schema.SetAttribute); !ok || !field.IsOptional() || !field.IsComputed() || field.Default == nil {
		t.Fatalf("integrations must be an empty-default set: %#v", response.Schema.Attributes["integrations"])
	}
	if field, ok := response.Schema.Attributes["configured_components"].(schema.ListNestedAttribute); !ok || !field.IsRequired() {
		t.Fatalf("configured_components must be a required list: %#v", response.Schema.Attributes["configured_components"])
	}
	if field, ok := response.Schema.Attributes["effective_components"].(schema.ListNestedAttribute); !ok || !field.IsComputed() {
		t.Fatalf("effective_components must be computed: %#v", response.Schema.Attributes["effective_components"])
	}
	if _, ok := response.Schema.Blocks["timeouts"].(schema.SingleNestedBlock); !ok {
		t.Fatalf("timeouts block missing: %#v", response.Schema.Blocks)
	}
}

func TestRecipeVersionValidate(t *testing.T) {
	cases := []struct {
		name string
		edit func(*recipeVersionModel)
		want string
	}{
		{"valid", func(*recipeVersionModel) {}, ""},
		{"release type", func(m *recipeVersionModel) { m.ReleaseType = types.StringValue("HOTFIX") }, "release_type must be MAJOR"},
		{"volume low", func(m *recipeVersionModel) { m.VolumeSize = types.Int64Value(7) }, "between 8 and 500"},
		{"volume high", func(m *recipeVersionModel) { m.VolumeSize = types.Int64Value(501) }, "between 8 and 500"},
		{"description", func(m *recipeVersionModel) { m.Description = types.StringValue(" ") }, "description must be non-empty"},
		{"component name", func(m *recipeVersionModel) {
			m.ConfiguredComponents = versionComponents(t, vewrecipes.ComponentVersion{ComponentID: "id", ComponentName: " ", VersionID: "version", VersionName: "name", Type: "APP", Order: 1})
		}, "component_name must be non-empty"},
		{"nonpositive order", func(m *recipeVersionModel) {
			m.ConfiguredComponents = versionComponents(t, vewrecipes.ComponentVersion{ComponentID: "id", ComponentName: "name", VersionID: "version", VersionName: "name", Type: "APP", Order: 0})
		}, "order must be positive"},
		{"duplicate order", func(m *recipeVersionModel) {
			m.ConfiguredComponents = versionComponents(t, vewrecipes.ComponentVersion{ComponentID: "one", ComponentName: "one", VersionID: "v1", VersionName: "v1", Type: "APP", Order: 1}, vewrecipes.ComponentVersion{ComponentID: "two", ComponentName: "two", VersionID: "v2", VersionName: "v2", Type: "HELPER", Order: 1})
		}, "order must be unique"},
		{"empty integration", func(m *recipeVersionModel) {
			m.Integrations = types.SetValueMust(types.StringType, []attr.Value{types.StringValue(" ")})
		}, "non-empty values"},
		{"bad timeout", func(m *recipeVersionModel) {
			m.Timeouts = types.ObjectValueMust(recipeTimeoutTypes, map[string]attr.Value{"create": types.StringValue("later"), "update": types.StringNull(), "delete": types.StringNull()})
		}, "positive Go duration"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			model := validRecipeVersionModel(t)
			tc.edit(&model)
			state := recipeVersionState(t, model)
			request := resource.ValidateConfigRequest{Config: tfsdk.Config{Schema: state.Schema, Raw: state.Raw}}
			var response resource.ValidateConfigResponse
			(&recipeVersionResource{}).ValidateConfig(context.Background(), request, &response)
			if tc.want == "" && response.Diagnostics.HasError() {
				t.Fatalf("unexpected diagnostics: %v", response.Diagnostics)
			}
			if tc.want != "" {
				found := false
				for _, diagnostic := range response.Diagnostics {
					found = found || strings.Contains(diagnostic.Summary(), tc.want) || strings.Contains(diagnostic.Detail(), tc.want)
				}
				if !found {
					t.Fatalf("want %q in diagnostics: %v", tc.want, response.Diagnostics)
				}
			}
		})
	}
}

func TestRecipeVersionConversion(t *testing.T) {
	model := validRecipeVersionModel(t)
	model.ConfiguredComponents = versionComponents(t,
		vewrecipes.ComponentVersion{ComponentID: "second", ComponentName: "second", VersionID: "v2", VersionName: "v2", Type: "HELPER", Order: 2},
		vewrecipes.ComponentVersion{ComponentID: "first", ComponentName: "first", VersionID: "v1", VersionName: "v1", Type: "APP", Order: 1},
	)
	input, diagnostics := recipeVersionInput(context.Background(), model)
	if diagnostics.HasError() || input.VolumeSize != "20" || len(input.Components) != 2 || input.Components[0].ComponentID != "second" || input.Components[1].ComponentID != "first" || strings.Join(input.Integrations, ",") != "a,z" {
		t.Fatalf("conversion input/diagnostics = %#v/%v", input, diagnostics)
	}
}
