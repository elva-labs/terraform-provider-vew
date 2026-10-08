package projectaccess

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	api "github.com/elva-labs/terraform-provider-vew/internal/vew/projectaccess"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type fakeAPI struct {
	api.API
	project        api.Project
	user           api.UserAssignment
	group          api.GroupAssignment
	client         api.ClientAssignment
	created        int
	key            string
	update         int
	deleted        int
	failCreateOnce bool
	readErr        error
}

func (f *fakeAPI) CreateProject(_ context.Context, in api.ProjectInput, key string) (string, error) {
	f.created++
	if f.key != "" && f.key != key {
		panic("idempotency key changed")
	}
	f.key = key
	if f.failCreateOnce && f.created == 1 {
		return "", errors.New("lost response")
	}
	f.project = api.Project{ID: "proj-1", Name: in.Name, Description: in.Description, IsActive: in.IsActive}
	return f.project.ID, nil
}
func (f *fakeAPI) GetProject(context.Context, string) (api.Project, error) {
	return f.project, f.readErr
}
func (f *fakeAPI) UpdateProject(_ context.Context, _ string, in api.ProjectInput) error {
	f.update++
	f.project.Name = in.Name
	f.project.Description = in.Description
	f.project.IsActive = in.IsActive
	return nil
}
func (f *fakeAPI) DeactivateProject(context.Context, string) error {
	f.deleted++
	f.project.IsActive = false
	return nil
}
func (f *fakeAPI) CreateUser(_ context.Context, pid, uid string, in api.UserInput) error {
	f.user = api.UserAssignment{ProjectID: pid, UserID: uid, Roles: in.Roles, Email: in.Email, DisplayName: in.DisplayName}
	return nil
}
func (f *fakeAPI) GetUser(context.Context, string, string) (api.UserAssignment, error) {
	return f.user, f.readErr
}
func (f *fakeAPI) UpdateUser(_ context.Context, _ string, _ string, in api.UserInput) error {
	f.update++
	f.user.Roles = in.Roles
	f.user.Email = in.Email
	f.user.DisplayName = in.DisplayName
	return nil
}
func (f *fakeAPI) DeleteUser(context.Context, string, string) error { f.deleted++; return nil }
func (f *fakeAPI) PutGroup(_ context.Context, pid, gid string, in api.GroupInput) error {
	f.update++
	f.group = api.GroupAssignment{ProjectID: pid, GroupID: gid, Roles: in.Roles, GroupName: in.GroupName}
	return nil
}
func (f *fakeAPI) GetGroup(context.Context, string, string) (api.GroupAssignment, error) {
	return f.group, f.readErr
}
func (f *fakeAPI) DeleteGroup(context.Context, string, string) error { f.deleted++; return nil }
func (f *fakeAPI) ActivateClient(_ context.Context, pid, cid string) error {
	f.update++
	f.client = api.ClientAssignment{ProjectID: pid, ClientID: cid, Status: "ACTIVE"}
	return nil
}
func (f *fakeAPI) GetClient(context.Context, string, string) (api.ClientAssignment, error) {
	return f.client, f.readErr
}
func (f *fakeAPI) RevokeClient(context.Context, string, string) error {
	f.deleted++
	f.client.Status = "REVOKED"
	return nil
}
func state(t *testing.T, r *accessResource, v any) tfsdk.State {
	t.Helper()
	var sr resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &sr)
	s := tfsdk.State{Schema: sr.Schema}
	if d := s.Set(context.Background(), v); d.HasError() {
		t.Fatal(d)
	}
	return s
}
func TestProjectCreateRetryDriftAndDeactivate(t *testing.T) {
	ctx := context.Background()
	f := &fakeAPI{failCreateOnce: true}
	r := &accessResource{kind: "project", client: f}
	plan := state(t, r, &projectModel{ID: types.StringUnknown(), Name: types.StringValue("test"), Description: types.StringNull(), IsActive: types.BoolValue(true), CreatedAt: types.StringUnknown(), UpdatedAt: types.StringUnknown()})
	out := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan{Schema: plan.Schema, Raw: plan.Raw}}, &out)
	if out.Diagnostics.HasError() || f.created != 2 || f.key == "" {
		t.Fatalf("create retry: %v %d %q", out.Diagnostics, f.created, f.key)
	}
	f.project.Name = "drifted"
	read := resource.ReadResponse{State: out.State}
	r.Read(ctx, resource.ReadRequest{State: out.State}, &read)
	var got projectModel
	if d := read.State.Get(ctx, &got); d.HasError() || got.Name.ValueString() != "drifted" {
		t.Fatalf("read drift: %v %#v", d, got)
	}
	f.project.IsActive = false
	read = resource.ReadResponse{State: read.State}
	r.Read(ctx, resource.ReadRequest{State: read.State}, &read)
	read.State.Get(ctx, &got)
	if got.IsActive.ValueBool() {
		t.Fatal("inactive project was hidden")
	}
	del := resource.DeleteResponse{State: read.State}
	r.Delete(ctx, resource.DeleteRequest{State: read.State}, &del)
	if del.Diagnostics.HasError() || f.deleted != 1 {
		t.Fatalf("deactivate: %v", del.Diagnostics)
	}
}
func TestAssignmentLifecycleAndSafeDiagnostics(t *testing.T) {
	ctx := context.Background()
	email := "user@example.test"
	name := "Example User"
	f := &fakeAPI{}
	r := &accessResource{kind: "assignment", client: f}
	m := userModel{ID: types.StringUnknown(), ProjectID: types.StringValue("proj-1"), UserID: types.StringValue("ABC-123"), Roles: types.SetValueMust(types.StringType, []attr.Value{types.StringValue("ADMIN")}), Email: types.StringValue(email), DisplayName: types.StringValue(name)}
	plan := state(t, r, &m)
	out := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan{Schema: plan.Schema, Raw: plan.Raw}}, &out)
	if out.Diagnostics.HasError() || f.user.Email == nil || *f.user.Email != email {
		t.Fatalf("create assignment: %v %#v", out.Diagnostics, f.user)
	}
	f.user.Roles = []string{"PLATFORM_USER"}
	read := resource.ReadResponse{State: out.State}
	r.Read(ctx, resource.ReadRequest{State: out.State}, &read)
	var got userModel
	read.State.Get(ctx, &got)
	var rr []string
	got.Roles.ElementsAs(ctx, &rr, false)
	if len(rr) != 1 || rr[0] != "PLATFORM_USER" {
		t.Fatalf("role drift: %v", rr)
	}
	apiErr := &vew.APIError{Status: 403, Problem: vew.Problem{Detail: "Bearer secret raw-claim", Code: "SECRET"}}
	var d resource.ReadResponse
	d.State = read.State
	f.readErr = apiErr
	r.Read(ctx, resource.ReadRequest{State: read.State}, &d)
	if !d.Diagnostics.HasError() || strings.Contains(d.Diagnostics.Errors()[0].Detail(), "secret") || strings.Contains(d.Diagnostics.Errors()[0].Detail(), "raw-claim") {
		t.Fatalf("unsafe diagnostic: %v", d.Diagnostics)
	}
}
func TestGroupAndClientImportAndLifecycle(t *testing.T) {
	ctx := context.Background()
	gid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	for _, tc := range []struct {
		kind, id string
		initial  any
	}{{"group_assignment", "proj-1/" + strings.ToUpper(gid), groupModel{ID: types.StringUnknown(), ProjectID: types.StringValue("proj-1"), GroupID: types.StringValue(gid), Roles: types.SetValueMust(types.StringType, []attr.Value{types.StringValue("ADMIN")})}}, {"client_assignment", "proj-1/client-1", clientModel{ID: types.StringUnknown(), ProjectID: types.StringValue("proj-1"), ClientID: types.StringValue("client-1"), Status: types.StringValue("ACTIVE")}}} {
		t.Run(tc.kind, func(t *testing.T) {
			f := &fakeAPI{}
			r := &accessResource{kind: tc.kind, client: f}
			plan := state(t, r, tc.initial)
			out := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema}}
			r.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan{Schema: plan.Schema, Raw: plan.Raw}}, &out)
			if out.Diagnostics.HasError() {
				t.Fatal(out.Diagnostics)
			}
			imported := resource.ImportStateResponse{State: out.State}
			r.ImportState(ctx, resource.ImportStateRequest{ID: tc.id}, &imported)
			if imported.Diagnostics.HasError() {
				t.Fatal(imported.Diagnostics)
			}
			read := resource.ReadResponse{State: imported.State}
			r.Read(ctx, resource.ReadRequest{State: imported.State}, &read)
			if read.Diagnostics.HasError() {
				t.Fatal(read.Diagnostics)
			}
			del := resource.DeleteResponse{State: out.State}
			r.Delete(ctx, resource.DeleteRequest{State: out.State}, &del)
			if del.Diagnostics.HasError() || f.deleted != 1 {
				t.Fatalf("delete: %v %d", del.Diagnostics, f.deleted)
			}
			if tc.kind == "client_assignment" && f.client.Status != "REVOKED" {
				t.Fatal("client was not revoked")
			}
		})
	}
}

func TestProjectCreateRetainsAcceptedIDWhenReadFails(t *testing.T) {
	ctx := context.Background()
	f := &fakeAPI{readErr: &vew.APIError{Status: 404}}
	r := &accessResource{kind: "project", client: f}
	plan := state(t, r, &projectModel{ID: types.StringUnknown(), Name: types.StringValue("test"), Description: types.StringNull(), IsActive: types.BoolValue(true), CreatedAt: types.StringUnknown(), UpdatedAt: types.StringUnknown()})
	out := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan{Schema: plan.Schema, Raw: plan.Raw}}, &out)
	var got projectModel
	d := out.State.Get(ctx, &got)
	if !out.Diagnostics.HasError() || d.HasError() || got.ID.ValueString() != "proj-1" {
		t.Fatalf("accepted ID lost: diagnostics=%v state=%#v state diagnostics=%v", out.Diagnostics, got, d)
	}
}

func TestGroupNameIsSentAndRead(t *testing.T) {
	ctx := context.Background()
	gid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	name := "vew-shared-users"
	groupAPI := &fakeAPI{group: api.GroupAssignment{ProjectID: "proj-1", GroupID: gid, Roles: []string{"PLATFORM_USER"}, GroupName: &name}}
	group := &accessResource{kind: "group_assignment", client: groupAPI}
	roles := types.SetValueMust(types.StringType, []attr.Value{types.StringValue("PLATFORM_USER")})
	planned := state(t, group, &groupModel{ID: types.StringValue("proj-1/" + gid), ProjectID: types.StringValue("proj-1"), GroupID: types.StringValue(gid), Roles: roles, GroupName: types.StringValue(name)})
	update := resource.UpdateResponse{State: planned}
	group.Update(ctx, resource.UpdateRequest{Plan: tfsdk.Plan{Schema: planned.Schema, Raw: planned.Raw}, State: planned}, &update)
	if update.Diagnostics.HasError() || groupAPI.group.GroupName == nil || *groupAPI.group.GroupName != name {
		t.Fatalf("group_name not sent: %v %#v", update.Diagnostics, groupAPI.group)
	}
	unnamed := state(t, group, &groupModel{ID: types.StringValue("proj-1/" + gid), ProjectID: types.StringValue("proj-1"), GroupID: types.StringValue(gid), Roles: roles, GroupName: types.StringNull()})
	read := resource.ReadResponse{State: unnamed}
	group.Read(ctx, resource.ReadRequest{State: unnamed}, &read)
	var got groupModel
	read.State.Get(ctx, &got)
	if read.Diagnostics.HasError() || got.GroupName.ValueString() != name {
		t.Fatalf("group_name not read: %v %#v", read.Diagnostics, got)
	}
}

func TestGroupRoleDriftAndClientRevocationRecovery(t *testing.T) {
	ctx := context.Background()
	gid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	groupAPI := &fakeAPI{group: api.GroupAssignment{ProjectID: "proj-1", GroupID: gid, Roles: []string{"PLATFORM_USER"}}}
	group := &accessResource{kind: "group_assignment", client: groupAPI}
	groupState := state(t, group, &groupModel{ID: types.StringValue("proj-1/" + gid), ProjectID: types.StringValue("proj-1"), GroupID: types.StringValue(gid), Roles: types.SetValueMust(types.StringType, []attr.Value{types.StringValue("ADMIN")})})
	read := resource.ReadResponse{State: groupState}
	group.Read(ctx, resource.ReadRequest{State: groupState}, &read)
	var drift groupModel
	read.State.Get(ctx, &drift)
	var got []string
	drift.Roles.ElementsAs(ctx, &got, false)
	if len(got) != 1 || got[0] != "PLATFORM_USER" {
		t.Fatalf("group drift not detected: %v", got)
	}
	update := resource.UpdateResponse{State: read.State}
	group.Update(ctx, resource.UpdateRequest{Plan: tfsdk.Plan{Schema: groupState.Schema, Raw: groupState.Raw}, State: read.State}, &update)
	if update.Diagnostics.HasError() || groupAPI.group.Roles[0] != "ADMIN" {
		t.Fatalf("group reconciliation: %v %#v", update.Diagnostics, groupAPI.group)
	}
	clientAPI := &fakeAPI{client: api.ClientAssignment{ProjectID: "proj-1", ClientID: "client-1", Status: "REVOKED"}}
	client := &accessResource{kind: "client_assignment", client: clientAPI}
	clientState := state(t, client, &clientModel{ID: types.StringValue("proj-1/client-1"), ProjectID: types.StringValue("proj-1"), ClientID: types.StringValue("client-1"), Status: types.StringValue("ACTIVE")})
	clientRead := resource.ReadResponse{State: clientState}
	client.Read(ctx, resource.ReadRequest{State: clientState}, &clientRead)
	var revoked clientModel
	clientRead.State.Get(ctx, &revoked)
	if revoked.Status.ValueString() != "REVOKED" {
		t.Fatalf("revocation drift not detected: %#v", revoked)
	}
	restored := resource.UpdateResponse{State: clientRead.State}
	client.Update(ctx, resource.UpdateRequest{Plan: tfsdk.Plan{Schema: clientState.Schema, Raw: clientState.Raw}, State: clientRead.State}, &restored)
	if restored.Diagnostics.HasError() || clientAPI.client.Status != "ACTIVE" || clientAPI.client.ClientID != "client-1" {
		t.Fatalf("client reactivation: %v %#v", restored.Diagnostics, clientAPI.client)
	}
}
func TestProjectExperienceIsSentAndRead(t *testing.T) {
	ctx := context.Background()
	f := &fakeAPI{}
	r := &accessResource{kind: "project", client: f}
	plan := state(t, r, &projectModel{ID: types.StringUnknown(), Name: types.StringValue("test"), Description: types.StringNull(), IsActive: types.BoolValue(true), RemoteSupport: types.BoolNull(), Experience: types.StringValue("workbench-only"), CreatedAt: types.StringUnknown(), UpdatedAt: types.StringUnknown()})
	if in := r.projectInput(model{Name: types.StringValue("test"), IsActive: types.BoolValue(true), Experience: types.StringValue("workbench-only")}); in.Experience == nil || *in.Experience != "workbench-only" {
		t.Fatalf("experience not sent: %#v", in)
	}
	if in := r.projectInput(model{Name: types.StringValue("test"), IsActive: types.BoolValue(true), Experience: types.StringUnknown()}); in.Experience != nil {
		t.Fatalf("an unset experience must be omitted: %#v", in)
	}
	out := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan{Schema: plan.Schema, Raw: plan.Raw}}, &out)
	if out.Diagnostics.HasError() {
		t.Fatal(out.Diagnostics)
	}
	value := "workbench-only"
	f.project.Experience = &value
	read := resource.ReadResponse{State: out.State}
	r.Read(ctx, resource.ReadRequest{State: out.State}, &read)
	var got projectModel
	if d := read.State.Get(ctx, &got); d.HasError() || got.Experience.ValueString() != "workbench-only" {
		t.Fatalf("read experience: %v %#v", d, got)
	}
}
func TestValidExperience(t *testing.T) {
	for v, want := range map[string]bool{"full": true, "workbench-only": true, "kiosk": false, "": false} {
		if validExperience(v) != want {
			t.Errorf("validExperience(%q) = %v", v, !want)
		}
	}
}
func TestProjectIDIsStableAcrossUpdates(t *testing.T) {
	r := &accessResource{kind: "project"}
	var sr resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &sr)
	for _, name := range []string{"id", "created_at"} {
		attr, ok := sr.Schema.Attributes[name].(schema.StringAttribute)
		if !ok || len(attr.PlanModifiers) == 0 {
			t.Fatalf("%s must keep its state value on update (UseStateForUnknown)", name)
		}
	}
}
