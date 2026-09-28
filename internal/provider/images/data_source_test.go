package images

import (
	"context"
	"strings"
	"testing"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	vewimages "github.com/elva-labs/terraform-provider-vew/internal/vew/images"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestImageReadDiagnosticsOmitUnsafeDetails(t *testing.T) {
	const secret = "bearer-top-secret raw-body private-detail"
	var diagnostics diag.Diagnostics
	addReadError(&diagnostics, "read image", "image-1", &vew.APIError{Status: 403, Problem: vew.Problem{Code: "ACCESS_DENIED", RequestID: "req-1", Detail: secret}})
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	detail := diagnostics[0].Detail()
	if !strings.Contains(detail, "403") || !strings.Contains(detail, "ACCESS_DENIED") || !strings.Contains(detail, "req-1") || !strings.Contains(detail, "image-1") {
		t.Fatalf("safe metadata missing: %s", detail)
	}
	if strings.Contains(detail, "top-secret") || strings.Contains(detail, "raw-body") || strings.Contains(detail, "private-detail") {
		t.Fatalf("unsafe detail leaked: %s", detail)
	}
}
func TestImageFiltersAndSafeDiagnosticHelpers(t *testing.T) {
	remote := []vewimages.Image{
		{ID: "z", PipelineID: "p", Status: "RETIRED"},
		{ID: "a", PipelineID: "p", Status: "FAILED"},
		{ID: "b", PipelineID: "other", Status: "FAILED"},
	}
	for _, tc := range []struct {
		pipeline, status string
		want             []string
	}{
		{"", "", []string{"a", "b", "z"}},
		{"p", "", []string{"a", "z"}},
		{"", "FAILED", []string{"a", "b"}},
		{"p", "RETIRED", []string{"z"}},
	} {
		pipeline, status := types.StringNull(), types.StringNull()
		if tc.pipeline != "" {
			pipeline = types.StringValue(tc.pipeline)
		}
		if tc.status != "" {
			status = types.StringValue(tc.status)
		}
		got, err := filterAndSortImages(remote, pipeline, status)
		if err != nil || len(got) != len(tc.want) {
			t.Fatalf("filters (%q,%q) = %#v, %v", tc.pipeline, tc.status, got, err)
		}
		for i := range got {
			if got[i].ID != tc.want[i] {
				t.Fatalf("filters (%q,%q) sorted IDs = %#v", tc.pipeline, tc.status, got)
			}
		}
	}
	if _, err := filterAndSortImages([]vewimages.Image{{}}, types.StringNull(), types.StringNull()); err == nil {
		t.Fatal("accepted image missing ID")
	}
	if !nullable(nil).IsNull() {
		t.Fatal("null upstream value is not null")
	}
	for _, status := range []string{"CREATING", "CREATED", "FAILED", "RETIRED", "DELETED"} {
		if !knownStatus(status) {
			t.Fatalf("status %q not recognized", status)
		}
	}
	if knownStatus("NOPE") {
		t.Fatal("unsupported status recognized")
	}
}

func TestImageDataSourceSchemas(t *testing.T) {
	var exact datasource.SchemaResponse
	NewImageDataSource().Schema(context.Background(), datasource.SchemaRequest{}, &exact)
	for _, name := range []string{"project_id", "image_id"} {
		field, ok := exact.Schema.Attributes[name].(schema.StringAttribute)
		if !ok || !field.IsRequired() || field.IsComputed() {
			t.Fatalf("%s must be a required selector", name)
		}
	}
	for _, name := range []string{"pipeline_id", "status", "image_upstream_id"} {
		if !exact.Schema.Attributes[name].IsComputed() {
			t.Fatalf("%s must be computed", name)
		}
	}
	var collection datasource.SchemaResponse
	NewImagesDataSource().Schema(context.Background(), datasource.SchemaRequest{}, &collection)
	project, ok := collection.Schema.Attributes["project_id"].(schema.StringAttribute)
	if !ok || !project.IsRequired() {
		t.Fatal("project_id must be required")
	}
	for _, name := range []string{"pipeline_id", "status"} {
		field, ok := collection.Schema.Attributes[name].(schema.StringAttribute)
		if !ok || !field.IsOptional() {
			t.Fatalf("%s must be optional", name)
		}
	}
	if !collection.Schema.Attributes["images"].IsComputed() {
		t.Fatal("images must be computed")
	}
	list, ok := collection.Schema.Attributes["images"].(schema.ListNestedAttribute)
	if !ok || !list.NestedObject.Attributes["project_id"].IsComputed() {
		t.Fatal("each listed image must expose project_id")
	}
}

func TestImageSelectorsRejectNullUnknownAndEmpty(t *testing.T) {
	for _, value := range []types.String{types.StringNull(), types.StringUnknown(), types.StringValue("  ")} {
		var diagnostics diag.Diagnostics
		validateRequired(&diagnostics, "image_id", value, false)
		if !diagnostics.HasError() {
			t.Fatalf("accepted invalid selector %#v", value)
		}
	}
}

func TestImageRequiredUnknownSelectorsAreDeferredUntilRead(t *testing.T) {
	for _, name := range []string{"project_id", "image_id"} {
		var diagnostics diag.Diagnostics
		if !validateRequired(&diagnostics, name, types.StringUnknown(), true) || diagnostics.HasError() {
			t.Fatalf("unknown %s should be deferred during ValidateConfig: %v", name, diagnostics)
		}
		var readDiagnostics diag.Diagnostics
		if validateRequired(&readDiagnostics, name, types.StringUnknown(), false) || !readDiagnostics.HasError() {
			t.Fatalf("unknown %s should fail at Read", name)
		}
	}
}

func TestImageCollectionFiltersRejectUnknownAndMalformedValues(t *testing.T) {
	for _, tc := range []struct {
		name             string
		pipeline, status types.String
	}{
		{"unknown pipeline", types.StringUnknown(), types.StringNull()},
		{"unknown status", types.StringNull(), types.StringUnknown()},
		{"whitespace pipeline", types.StringValue(" pipe "), types.StringNull()},
		{"invalid pipeline", types.StringValue("../pipe"), types.StringNull()},
		{"empty pipeline", types.StringValue(" "), types.StringNull()},
		{"unsupported status", types.StringNull(), types.StringValue("UNKNOWN")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var diagnostics diag.Diagnostics
			if validateCollectionFilters(&diagnostics, tc.pipeline, tc.status, true) || !diagnostics.HasError() {
				t.Fatal("invalid filters were accepted")
			}
		})
	}
	var noFilters diag.Diagnostics
	if !validateCollectionFilters(&noFilters, types.StringNull(), types.StringNull(), true) || noFilters.HasError() {
		t.Fatalf("null optional filters should be accepted: %v", noFilters)
	}
}

func TestImageIdentityValidation(t *testing.T) {
	for _, tc := range []struct {
		name        string
		image       vewimages.Image
		project, id string
		wantErr     bool
	}{
		{"matching", vewimages.Image{ProjectID: "p", ID: "i"}, "p", "i", false},
		{"missing project allowed", vewimages.Image{ID: "i"}, "p", "i", false},
		{"wrong project", vewimages.Image{ProjectID: "other", ID: "i"}, "p", "i", true},
		{"wrong exact ID", vewimages.Image{ProjectID: "p", ID: "other"}, "p", "i", true},
		{"missing exact ID", vewimages.Image{ProjectID: "p"}, "p", "i", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateImageIdentity(tc.image, tc.project, tc.id); (err != nil) != tc.wantErr {
				t.Fatalf("identity error = %v", err)
			}
		})
	}
}

func TestImageListValuePreservesEmptyAndNullUpstreamAndProject(t *testing.T) {
	ctx := context.Background()
	empty, diagnostics := imageListValue(ctx, nil, "project")
	if diagnostics.HasError() || empty.IsNull() || len(empty.Elements()) != 0 {
		t.Fatalf("empty collection = %v, %v", empty, diagnostics)
	}
	items, diagnostics := imageListValue(ctx, []vewimages.Image{{ID: "image", PipelineID: "pipeline", Status: "DELETED"}}, "project")
	if diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	var values []imageListItem
	if d := items.ElementsAs(ctx, &values, false); d.HasError() {
		t.Fatal(d)
	}
	if len(values) != 1 || values[0].ProjectID.ValueString() != "project" || !values[0].UpstreamID.IsNull() {
		t.Fatalf("image list item = %#v", values)
	}
}
