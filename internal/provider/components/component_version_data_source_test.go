package components

import (
	"context"
	"testing"

	vewcomponents "github.com/elva-labs/terraform-provider-vew/internal/vew/components"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
)

func TestComponentVersionDataSourceSchema(t *testing.T) {
	ds := NewComponentVersionDataSource()
	var response datasource.SchemaResponse
	ds.Schema(context.Background(), datasource.SchemaRequest{}, &response)
	for _, name := range []string{"project_id", "component_id", "version_id"} {
		a, ok := response.Schema.Attributes[name].(schema.StringAttribute)
		if !ok || !a.IsRequired() || a.IsComputed() {
			t.Fatalf("%s must be required selector: %#v", name, response.Schema.Attributes[name])
		}
	}
	if _, ok := response.Schema.Attributes["release_type"]; ok {
		t.Fatal("release_type is not observable and must not be exposed")
	}
	if _, ok := response.Schema.Attributes["timeouts"]; ok {
		t.Fatal("timeouts are resource-only")
	}
	for _, name := range []string{"definition_json", "dependencies", "status", "license_dashboard", "notes"} {
		if !response.Schema.Attributes[name].IsComputed() {
			t.Fatalf("%s must be computed", name)
		}
	}
}

func TestComponentVersionResponseMatchesSelectors(t *testing.T) {
	valid := vewcomponents.ComponentVersion{ID: "v1", ComponentID: "c1"}
	if !componentVersionResponseMatchesSelectors(valid, "c1", "v1") {
		t.Fatal("matching response rejected")
	}
	for _, test := range []struct {
		version                vewcomponents.ComponentVersion
		componentID, versionID string
	}{
		{vewcomponents.ComponentVersion{ID: "v2", ComponentID: "c1"}, "c1", "v1"},
		{vewcomponents.ComponentVersion{ID: "v1", ComponentID: "c2"}, "c1", "v1"},
		{vewcomponents.ComponentVersion{ID: "v1", ComponentID: "bad/id"}, "bad/id", "v1"},
	} {
		if componentVersionResponseMatchesSelectors(test.version, test.componentID, test.versionID) {
			t.Fatalf("mismatched response accepted: %#v", test)
		}
	}
}
