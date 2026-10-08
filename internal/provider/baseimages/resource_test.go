package baseimages

import (
	"context"
	"testing"

	api "github.com/elva-labs/terraform-provider-vew/internal/vew/baseimages"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type stub struct {
	releases []api.ReleaseInput
	current  api.BaseImage
}

func ptr(s string) *string { return &s }

func (s *stub) GetBaseImage(context.Context, string, string) (api.BaseImage, error) {
	return s.current, nil
}
func (s *stub) ReleaseBaseImage(_ context.Context, arch, channel string, in api.ReleaseInput) (api.BaseImage, error) {
	s.releases = append(s.releases, in)
	s.current = api.BaseImage{Architecture: arch, Channel: channel, OSVersion: "Base OS", ParameterName: "/base/" + channel + "/" + arch,
		Status: "RELEASED", ProjectID: ptr(in.ProjectID), ImageID: ptr(in.ImageID), AmiID: ptr("ami-" + in.ImageID), PreviousAmiID: ptr("ami-old")}
	return s.current, nil
}

func TestReleaseCreateUpdateAndDeleteOnlyForgets(t *testing.T) {
	ctx := context.Background()
	s := &stub{}
	r := &releaseResource{client: s}
	var sr resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sr)
	plan := tfsdk.State{Schema: sr.Schema}
	model := releaseModel{ID: types.StringUnknown(), ProjectID: types.StringValue("p1"), Architecture: types.StringValue("amd64"),
		Channel: types.StringValue("prod"), ImageID: types.StringValue("image-1"), OSVersion: types.StringUnknown(),
		ParameterName: types.StringUnknown(), AmiID: types.StringUnknown(), PreviousAmiID: types.StringUnknown()}
	plan.Set(ctx, &model)
	create := resource.CreateResponse{State: tfsdk.State{Schema: sr.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan{Schema: sr.Schema, Raw: plan.Raw}}, &create)
	var got releaseModel
	create.State.Get(ctx, &got)
	if create.Diagnostics.HasError() || got.ID.ValueString() != "amd64/prod" || got.AmiID.ValueString() != "ami-image-1" {
		t.Fatalf("create = %#v / %v", got, create.Diagnostics)
	}
	model.ImageID = types.StringValue("image-2")
	plan.Set(ctx, &model)
	update := resource.UpdateResponse{State: create.State}
	r.Update(ctx, resource.UpdateRequest{Plan: tfsdk.Plan{Schema: sr.Schema, Raw: plan.Raw}, State: create.State}, &update)
	if update.Diagnostics.HasError() || len(s.releases) != 2 || s.releases[1].ImageID != "image-2" {
		t.Fatalf("update releases = %#v / %v", s.releases, update.Diagnostics)
	}
	del := resource.DeleteResponse{State: update.State}
	r.Delete(ctx, resource.DeleteRequest{State: update.State}, &del)
	if len(s.releases) != 2 {
		t.Fatal("delete must not release or withdraw anything")
	}
}
