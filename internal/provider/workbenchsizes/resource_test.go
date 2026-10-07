package workbenchsizes

import (
	"context"
	"sort"
	"testing"

	api "github.com/elva-labs/terraform-provider-vew/internal/vew/workbenchsizes"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type stub struct {
	grants  map[string]api.Grant
	puts    int
	deletes int
	email   string
}

func (s *stub) List(context.Context, string) (api.Grants, error) {
	out := api.Grants{}
	for _, g := range s.grants {
		out.Grants = append(out.Grants, g)
	}
	return out, nil
}
func (s *stub) Get(_ context.Context, _ string, userID string) (api.Grant, error) {
	g, ok := s.grants[userID]
	if !ok {
		return api.Grant{}, api.ErrNotGranted
	}
	return g, nil
}
func (s *stub) Put(_ context.Context, _ string, userID string, sizes []string, email string) (api.Grant, error) {
	s.puts++
	s.email = email
	sorted := append([]string{}, sizes...)
	sort.Strings(sorted)
	stored := s.grants[userID].UserEmail
	if stored == "" {
		stored = email
	}
	g := api.Grant{UserID: userID, UserEmail: stored, Sizes: sorted, UpdatedBy: "terraform", UpdatedAt: "now"}
	s.grants[userID] = g
	return g, nil
}
func (s *stub) Delete(_ context.Context, _ string, userID string) error {
	s.deletes++
	delete(s.grants, userID)
	return nil
}

func set(t *testing.T, values ...string) types.Set {
	t.Helper()
	elems := make([]attr.Value, 0, len(values))
	for _, v := range values {
		elems = append(elems, types.StringValue(v))
	}
	s, d := types.SetValue(types.StringType, elems)
	if d.HasError() {
		t.Fatal(d)
	}
	return s
}

func plan(t *testing.T, r resource.Resource, m *sizesModel) tfsdk.Plan {
	t.Helper()
	var sr resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &sr)
	st := tfsdk.State{Schema: sr.Schema}
	if d := st.Set(context.Background(), m); d.HasError() {
		t.Fatalf("plan: %v", d)
	}
	return tfsdk.Plan{Schema: sr.Schema, Raw: st.Raw}
}

func TestCreateReadUpdateDelete(t *testing.T) {
	ctx := context.Background()
	s := &stub{grants: map[string]api.Grant{}}
	r := &sizesResource{client: s}
	m := &sizesModel{
		ID: types.StringUnknown(), ProjectID: types.StringValue("p1"), UserID: types.StringValue("U1"),
		UserEmail: types.StringValue("u1@saab"), Sizes: set(t, "standard-m", "disk-500"),
		UpdatedBy: types.StringUnknown(), UpdatedAt: types.StringUnknown(),
	}
	p := plan(t, r, m)
	create := resource.CreateResponse{State: tfsdk.State{Schema: p.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: p}, &create)
	var got sizesModel
	create.State.Get(ctx, &got)
	if create.Diagnostics.HasError() || got.ID.ValueString() != "p1/U1" || s.email != "u1@saab" || got.UpdatedBy.ValueString() != "terraform" {
		t.Fatalf("create = %#v / %v", got, create.Diagnostics)
	}

	read := resource.ReadResponse{State: create.State}
	r.Read(ctx, resource.ReadRequest{State: create.State}, &read)
	read.State.Get(ctx, &got)
	var sizes []string
	got.Sizes.ElementsAs(ctx, &sizes, false)
	if read.Diagnostics.HasError() || len(sizes) != 2 {
		t.Fatalf("read = %#v / %v", got, read.Diagnostics)
	}

	m.Sizes = set(t, "standard-l")
	up := plan(t, r, m)
	update := resource.UpdateResponse{State: tfsdk.State{Schema: up.Schema}}
	r.Update(ctx, resource.UpdateRequest{Plan: up}, &update)
	if update.Diagnostics.HasError() || s.grants["U1"].Sizes[0] != "standard-l" {
		t.Fatalf("update = %v / %#v", update.Diagnostics, s.grants)
	}

	del := resource.DeleteResponse{}
	r.Delete(ctx, resource.DeleteRequest{State: update.State}, &del)
	if del.Diagnostics.HasError() || s.deletes != 1 || len(s.grants) != 0 {
		t.Fatalf("delete = %v", del.Diagnostics)
	}

	gone := resource.ReadResponse{State: update.State}
	r.Read(ctx, resource.ReadRequest{State: update.State}, &gone)
	if !gone.State.Raw.IsNull() {
		t.Fatal("a removed grant must leave the state")
	}
}

func TestImportNeedsProjectAndUser(t *testing.T) {
	r := &sizesResource{}
	var sr resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &sr)
	for _, id := range []string{"p1", "/U1", "p1/", ""} {
		res := resource.ImportStateResponse{State: tfsdk.State{Schema: sr.Schema}}
		r.ImportState(context.Background(), resource.ImportStateRequest{ID: id}, &res)
		if !res.Diagnostics.HasError() {
			t.Fatalf("import %q accepted", id)
		}
	}
}
