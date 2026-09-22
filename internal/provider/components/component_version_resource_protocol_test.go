package components_test

import (
	"testing"

	"github.com/elva-labs/terraform-provider-vew/internal/provider/testhelpers"
	testresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestComponentVersionResourceCreationPlan(t *testing.T) {
	fake := testhelpers.NewVEWServer(t)
	testresource.Test(t, testresource.TestCase{
		IsUnitTest: true, ProtoV6ProviderFactories: testProtoV6ProviderFactories(),
		Steps: []testresource.TestStep{
			{Config: fake.ProviderConfig() + componentVersionResourceConfig(), PlanOnly: true, ExpectNonEmptyPlan: true},
		},
	})
}

func componentVersionResourceConfig() string {
	return `
resource "vew_component_version" "test" {
  project_id       = "prog-73488"
  component_id     = "cmp-123"
  description      = "created by provider test"
  release_type     = "PATCH"
  definition_json  = jsonencode({ phases = [] })
  software_vendor  = "VEW"
  software_version = "1.0"
}
`
}
