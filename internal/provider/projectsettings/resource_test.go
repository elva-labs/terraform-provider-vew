package projectsettings

import (
	"context"
	"testing"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	api "github.com/elva-labs/terraform-provider-vew/internal/vew/projectsettings"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type stub struct {
	management     *api.Management
	lifecycle      *api.WorkbenchLifecycle
	puts, deletes  int
	lastLifecycle  api.WorkbenchLifecycle
	lastManagement api.ManagementInput
}

func (s *stub) GetManagement(context.Context, string) (api.Management, error) {
	if s.management == nil {
		return api.Management{}, &vew.APIError{Status: 404}
	}
	return *s.management, nil
}
func (s *stub) PutManagement(_ context.Context, p string, in api.ManagementInput) error {
	s.puts++
	s.lastManagement = in
	s.management = &api.Management{ProjectID: p, ManagedBy: in.ManagedBy, Source: in.Source}
	return nil
}
func (s *stub) DeleteManagement(context.Context, string) error {
	s.deletes++
	s.management = nil
	return nil
}
func (s *stub) GetWorkbenchLifecycle(context.Context, string) (api.WorkbenchLifecycle, error) {
	if s.lifecycle == nil {
		return api.WorkbenchLifecycle{}, &vew.APIError{Status: 404}
	}
	return *s.lifecycle, nil
}
func (s *stub) PutWorkbenchLifecycle(_ context.Context, _ string, in api.WorkbenchLifecycle) error {
	s.puts++
	s.lastLifecycle = in
	s.lifecycle = &in
	return nil
}
func (s *stub) DeleteWorkbenchLifecycle(context.Context, string) error {
	s.deletes++
	s.lifecycle = nil
	return nil
}

func state(t *testing.T, r resource.Resource, model any) tfsdk.State {
	t.Helper()
	var sr resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &sr)
	st := tfsdk.State{Schema: sr.Schema}
	if d := st.Set(context.Background(), model); d.HasError() {
		t.Fatalf("state: %v", d)
	}
	return st
}

func TestManagementLifecycle(t *testing.T) {
	ctx := context.Background()
	s := &stub{}
	r := &managementResource{client: s}
	plan := state(t, r, &managementModel{ID: types.StringUnknown(), ProjectID: types.StringValue("p1"), ManagedBy: types.StringValue("terraform"), Source: types.StringValue("repo/programs/p1")})
	create := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan{Schema: plan.Schema, Raw: plan.Raw}}, &create)
	var m managementModel
	create.State.Get(ctx, &m)
	if create.Diagnostics.HasError() || m.ID.ValueString() != "p1" || s.lastManagement.Source != "repo/programs/p1" {
		t.Fatalf("create = %#v / %v", m, create.Diagnostics)
	}
	s.management = nil
	read := resource.ReadResponse{State: create.State}
	r.Read(ctx, resource.ReadRequest{State: create.State}, &read)
	if !read.State.Raw.IsNull() {
		t.Fatal("a removed mark must drop the resource from state")
	}
}

func TestWorkbenchLifecycleRoundTripKeepsUnsetFieldsNull(t *testing.T) {
	ctx := context.Background()
	s := &stub{}
	r := &lifecycleResource{client: s}
	model := lifecycleModel{
		ID: types.StringUnknown(), ProjectID: types.StringValue("p1"), AlwaysOn: types.BoolValue(false),
		IdleStopMinutes: types.Int64Null(), NightlyStop: types.BoolNull(), WeekendStop: types.BoolValue(true),
		AllowUserDisableNightlyStop: types.BoolValue(true), AllowUserIdleTimeout: types.BoolValue(true),
		UserIdleTimeoutMinMinutes: types.Int64Value(10), UserIdleTimeoutMaxMinutes: types.Int64Value(480),
	}
	plan := state(t, r, &model)
	create := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan{Schema: plan.Schema, Raw: plan.Raw}}, &create)
	if create.Diagnostics.HasError() || s.lastLifecycle.IdleStopMinutes != nil || s.lastLifecycle.NightlyStop != nil || !*s.lastLifecycle.WeekendStop {
		t.Fatalf("put = %#v / %v", s.lastLifecycle, create.Diagnostics)
	}
	read := resource.ReadResponse{State: create.State}
	r.Read(ctx, resource.ReadRequest{State: create.State}, &read)
	var got lifecycleModel
	read.State.Get(ctx, &got)
	if !got.IdleStopMinutes.IsNull() || !got.NightlyStop.IsNull() || got.UserIdleTimeoutMinMinutes.ValueInt64() != 10 {
		t.Fatalf("read = %#v", got)
	}
	del := resource.DeleteResponse{State: read.State}
	r.Delete(ctx, resource.DeleteRequest{State: read.State}, &del)
	if s.deletes != 1 || s.lifecycle != nil {
		t.Fatal("delete must return the project to the defaults")
	}
}

func TestWorkbenchLifecycleValidatesBounds(t *testing.T) {
	r := &lifecycleResource{}
	for name, model := range map[string]lifecycleModel{
		"idle too low":  {IdleStopMinutes: types.Int64Value(5), UserIdleTimeoutMinMinutes: types.Int64Value(30), UserIdleTimeoutMaxMinutes: types.Int64Value(480)},
		"min above max": {IdleStopMinutes: types.Int64Null(), UserIdleTimeoutMinMinutes: types.Int64Value(500), UserIdleTimeoutMaxMinutes: types.Int64Value(480)},
	} {
		model.ID, model.ProjectID = types.StringNull(), types.StringValue("p1")
		model.AlwaysOn, model.NightlyStop, model.WeekendStop = types.BoolNull(), types.BoolNull(), types.BoolNull()
		model.AllowUserDisableNightlyStop, model.AllowUserIdleTimeout = types.BoolNull(), types.BoolNull()
		cfg := state(t, r, &model)
		res := resource.ValidateConfigResponse{}
		r.ValidateConfig(context.Background(), resource.ValidateConfigRequest{Config: tfsdk.Config{Schema: cfg.Schema, Raw: cfg.Raw}}, &res)
		if !res.Diagnostics.HasError() {
			t.Errorf("%s: accepted", name)
		}
	}
}
