package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/provider"
)

func TestProviderMetadata(t *testing.T) {
	t.Parallel()

	var response provider.MetadataResponse
	New("test")().Metadata(context.Background(), provider.MetadataRequest{}, &response)

	if response.TypeName != "vew" {
		t.Fatalf("expected provider type name %q, got %q", "vew", response.TypeName)
	}
	if response.Version != "test" {
		t.Fatalf("expected provider version %q, got %q", "test", response.Version)
	}
}

func TestProviderSchema(t *testing.T) {
	t.Parallel()

	var response provider.SchemaResponse
	New("test")().Schema(context.Background(), provider.SchemaRequest{}, &response)

	wantAttributes := map[string]bool{
		"api_url":       true,
		"token_url":     true,
		"client_id":     true,
		"client_secret": true,
	}
	if len(response.Schema.Attributes) != len(wantAttributes) {
		t.Fatalf("expected %d provider attributes, got %d", len(wantAttributes), len(response.Schema.Attributes))
	}
	for name := range wantAttributes {
		attribute, ok := response.Schema.Attributes[name]
		if !ok {
			t.Fatalf("expected provider attribute %q", name)
		}
		if !attribute.IsOptional() {
			t.Fatalf("expected provider attribute %q to be optional", name)
		}
	}
	if !response.Schema.Attributes["client_secret"].IsSensitive() {
		t.Fatal("expected client_secret to be sensitive")
	}
}
