package pipelines

import (
	"context"
	"strings"
	"testing"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	vewpipelines "github.com/elva-labs/terraform-provider-vew/internal/vew/pipelines"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestPipelineDataSourceSchemas(t *testing.T) {
	t.Parallel()

	var exact datasource.SchemaResponse
	NewPipelineDataSource().Schema(context.Background(), datasource.SchemaRequest{}, &exact)
	for _, name := range []string{"project_id", "pipeline_id"} {
		attribute, ok := exact.Schema.Attributes[name].(schema.StringAttribute)
		if !ok || !attribute.IsRequired() || attribute.IsComputed() {
			t.Fatalf("%s must be a required selector", name)
		}
	}
	for _, name := range []string{"name", "description", "recipe_id", "recipe_name", "recipe_version_id", "recipe_version_name", "build_instance_types", "schedule", "product_id", "status", "distribution_config_arn", "infrastructure_config_arn", "pipeline_arn", "created_at", "created_by", "updated_at", "updated_by"} {
		if !exact.Schema.Attributes[name].IsComputed() {
			t.Fatalf("%s must be computed", name)
		}
	}
	if _, exists := exact.Schema.Attributes["id"]; exists {
		t.Fatal("generic id must not be exposed")
	}

	var collection datasource.SchemaResponse
	NewPipelinesDataSource().Schema(context.Background(), datasource.SchemaRequest{}, &collection)
	project, ok := collection.Schema.Attributes["project_id"].(schema.StringAttribute)
	if !ok || !project.IsRequired() {
		t.Fatal("project_id must be required")
	}
	if !collection.Schema.Attributes["pipelines"].IsComputed() {
		t.Fatal("pipelines must be computed")
	}
}

func TestPipelineSelectorValidation(t *testing.T) {
	t.Parallel()

	for _, value := range []types.String{
		types.StringNull(),
		types.StringUnknown(),
		types.StringValue(""),
		types.StringValue(" pipeline-1"),
		types.StringValue("../pipeline"),
	} {
		var diagnostics diag.Diagnostics
		if validateDataSourceSelector(&diagnostics, "pipeline_id", value, false) || !diagnostics.HasError() {
			t.Fatalf("accepted invalid read selector %#v", value)
		}
	}
	var diagnostics diag.Diagnostics
	if !validateDataSourceSelector(&diagnostics, "pipeline_id", types.StringUnknown(), true) || diagnostics.HasError() {
		t.Fatalf("configuration-time unknown should be deferred: %v", diagnostics)
	}
}

func TestPipelineDataSourceMappingPreservesTerminalAndNullableState(t *testing.T) {
	t.Parallel()

	upstreamProduct := "product-1"
	remotes := []vewpipelines.Pipeline{
		{ProjectID: "project-1", ID: "z-pipeline", ProductID: nil, PipelineARN: nil, Status: "RETIRED", BuildInstanceTypes: []string{"second", "first"}},
		{ProjectID: "project-1", ID: "a-pipeline", ProductID: &upstreamProduct, Status: "FAILED", BuildInstanceTypes: []string{}},
	}
	mapped, err := mapPipelineCollection(context.Background(), "project-1", remotes)
	if err != nil {
		t.Fatalf("map collection: %v", err)
	}
	if len(mapped) != 2 || mapped[0].PipelineID.ValueString() != "a-pipeline" || mapped[1].PipelineID.ValueString() != "z-pipeline" {
		t.Fatalf("pipeline order = %#v", mapped)
	}
	if mapped[0].Status.ValueString() != "FAILED" || mapped[1].Status.ValueString() != "RETIRED" {
		t.Fatalf("terminal status lost: %#v", mapped)
	}
	if mapped[0].ProductID.ValueString() != upstreamProduct || !mapped[1].ProductID.IsNull() || !mapped[1].PipelineARN.IsNull() {
		t.Fatalf("nullable fields changed: %#v", mapped)
	}
	var instances []string
	if diagnostics := mapped[1].BuildInstanceTypes.ElementsAs(context.Background(), &instances, false); diagnostics.HasError() {
		t.Fatalf("decode build instance types: %v", diagnostics)
	}
	if len(instances) != 2 || instances[0] != "second" || instances[1] != "first" {
		t.Fatalf("build instance order changed: %q", instances)
	}
	if len(remotes) != 2 || remotes[0].ID != "z-pipeline" {
		t.Fatal("collection mapping mutated the client response")
	}
	empty, err := mapPipelineCollection(context.Background(), "project-1", nil)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty collection = %#v, %v", empty, err)
	}
}

func TestPipelineDataSourceMappingRejectsMismatchedOrMissingIdentity(t *testing.T) {
	t.Parallel()

	for _, remote := range []vewpipelines.Pipeline{
		{ProjectID: "project-1"},
		{ProjectID: "other-project", ID: "pipeline-1"},
	} {
		if _, err := mapPipelineDataSource(context.Background(), "project-1", remote); err == nil {
			t.Fatalf("accepted invalid remote identity %#v", remote)
		}
	}
}

func TestPipelineDataSourceDiagnosticsAreSanitized(t *testing.T) {
	t.Parallel()

	secret := "Bearer token raw-response component-definition"
	var diagnostics diag.Diagnostics
	addPipelineDataSourceError(&diagnostics, "read", &vew.APIError{
		Status: 403,
		Problem: vew.Problem{
			Code:      "ACCESS_DENIED",
			RequestID: "request-1",
			Detail:    secret,
		},
	}, "project-1", "pipeline-1")
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	detail := diagnostics[0].Detail()
	for _, safe := range []string{"403", "ACCESS_DENIED", "request-1", "project-1", "pipeline-1"} {
		if !strings.Contains(detail, safe) {
			t.Fatalf("diagnostic missing %q: %s", safe, detail)
		}
	}
	for _, unsafe := range []string{"Bearer", "raw-response", "component-definition"} {
		if strings.Contains(detail, unsafe) {
			t.Fatalf("diagnostic leaked %q: %s", unsafe, detail)
		}
	}

	var unsafeMetadata diag.Diagnostics
	addPipelineDataSourceError(&unsafeMetadata, "read", &vew.APIError{
		Status: 500,
		Problem: vew.Problem{
			Code:      "bad code " + secret,
			RequestID: "bad/request/" + secret,
		},
	}, "bad/project", "bad/pipeline")
	detail = unsafeMetadata[0].Detail()
	if strings.Contains(detail, secret) || strings.Contains(detail, "bad/project") || strings.Contains(detail, "bad/pipeline") {
		t.Fatalf("diagnostic leaked unsafe metadata: %s", detail)
	}

	var notFound diag.Diagnostics
	addPipelineDataSourceError(&notFound, "read", &vew.APIError{Status: 404}, "project-1", "pipeline-1")
	if len(notFound) != 1 || !strings.Contains(notFound[0].Summary(), "not found") || !strings.Contains(notFound[0].Detail(), "pipeline-1") {
		t.Fatalf("not-found diagnostic = %#v", notFound)
	}
}
