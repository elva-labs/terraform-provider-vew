package components

import (
	"context"
	"strings"
	"testing"

	"github.com/elva-labs/terraform-provider-vew/internal/providerdata"
	vewcomponents "github.com/elva-labs/terraform-provider-vew/internal/vew/components"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

func TestComponentResourceSchema(t *testing.T) {
	r := NewComponentResource()
	var response resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &response)
	if len(response.Schema.Attributes) != 12 {
		t.Fatalf("attributes = %d, want 12", len(response.Schema.Attributes))
	}
}

func TestComponentResourceConfigureRequiresProviderData(t *testing.T) {
	r := NewComponentResource()
	var response resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: "wrong"}, &response)
	if !response.Diagnostics.HasError() || !strings.Contains(response.Diagnostics[0].Detail(), "providerdata.Data") {
		t.Fatalf("diagnostics = %v", response.Diagnostics)
	}
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: providerdata.Data{Components: componentAPIStub{}}}, &response)
	if r.client == nil {
		t.Fatal("component API was not configured")
	}
}

type componentAPIStub struct{}

func (componentAPIStub) CreateComponent(context.Context, string, vewcomponents.CreateComponentInput) (string, error) {
	return "", nil
}
func (componentAPIStub) GetComponent(context.Context, string, string) (vewcomponents.Component, error) {
	return vewcomponents.Component{}, nil
}
func (componentAPIStub) UpdateComponent(context.Context, string, string, vewcomponents.UpdateComponentInput) error {
	return nil
}
func (componentAPIStub) ArchiveComponent(context.Context, string, string) error { return nil }
