package mandatorycomponents

import (
	"context"
	"testing"

	api "github.com/elva-labs/terraform-provider-vew/internal/vew/mandatorycomponents"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type stub struct {
	puts    []api.PutInput
	deletes []string
	current *api.List
}

func (s *stub) GetList(context.Context, api.Key) (api.List, error) { return *s.current, nil }
func (s *stub) PutList(_ context.Context, k api.Key, in api.PutInput) (api.List, error) {
	s.puts = append(s.puts, in)
	l := api.List{Platform: k.Platform, OSVersion: k.OSVersion, Architecture: k.Architecture, Prepended: []api.Component{}, Appended: []api.Component{}}
	for _, c := range in.Prepended {
		l.Prepended = append(l.Prepended, api.Component{ComponentID: c.ComponentID, ComponentVersionID: c.ComponentVersionID, ComponentName: "n", ComponentVersionName: "1.0.0"})
	}
	for _, c := range in.Appended {
		l.Appended = append(l.Appended, api.Component{ComponentID: c.ComponentID, ComponentVersionID: c.ComponentVersionID, ComponentName: "n", ComponentVersionName: "1.0.0"})
	}
	s.current = &l
	return l, nil
}
func (s *stub) DeleteList(_ context.Context, _ api.Key, projectID string) error {
	s.deletes = append(s.deletes, projectID)
	return nil
}

func TestCreateKeepsAnOmittedListNullAndDeleteRemoves(t *testing.T) {
	ctx := context.Background()
	s := &stub{}
	r := &listResource{client: s}
	var sr resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sr)
	plan := tfsdk.State{Schema: sr.Schema}
	model := listModel{
		ID: types.StringUnknown(), ProjectID: types.StringValue("p1"), Platform: types.StringValue("Linux"),
		OSVersion: types.StringValue("Golden Ubuntu"), Architecture: types.StringValue("amd64"),
		Prepended:      []componentModel{{ComponentID: types.StringValue("c1"), ComponentVersionID: types.StringValue("v1")}},
		LastUpdateDate: types.StringUnknown(),
	}
	if d := plan.Set(ctx, &model); d.HasError() {
		t.Fatal(d)
	}
	create := resource.CreateResponse{State: tfsdk.State{Schema: sr.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan{Schema: sr.Schema, Raw: plan.Raw}}, &create)
	var got listModel
	create.State.Get(ctx, &got)
	if create.Diagnostics.HasError() || got.ID.ValueString() != "Linux/Golden Ubuntu/amd64" || len(got.Prepended) != 1 || got.Appended != nil {
		t.Fatalf("create = %#v / %v", got, create.Diagnostics)
	}
	if len(s.puts) != 1 || s.puts[0].ProjectID != "p1" || s.puts[0].Prepended[0].ComponentVersionID != "v1" {
		t.Fatalf("puts = %#v", s.puts)
	}
	del := resource.DeleteResponse{State: create.State}
	r.Delete(ctx, resource.DeleteRequest{State: create.State}, &del)
	if del.Diagnostics.HasError() || len(s.deletes) != 1 || s.deletes[0] != "p1" {
		t.Fatalf("delete = %#v / %v", s.deletes, del.Diagnostics)
	}
}
