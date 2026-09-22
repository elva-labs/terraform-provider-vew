package components_test

import (
	"testing"

	rootprovider "github.com/elva-labs/terraform-provider-vew/internal/provider"
	"github.com/elva-labs/terraform-provider-vew/internal/provider/testhelpers"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	testresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// The root-provider factory stays here rather than testhelpers so helpers
// never import the root provider and therefore cannot form an import cycle.
func testProtoV6ProviderFactories() map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){"vew": providerserver.NewProtocol6WithError(rootprovider.New("test")())}
}

func TestComponentResourceLifecycle(t *testing.T) {
	fake := testhelpers.NewVEWServer(t)
	testresource.Test(t, testresource.TestCase{IsUnitTest: true, ProtoV6ProviderFactories: testProtoV6ProviderFactories(), Steps: []testresource.TestStep{{Config: fake.ProviderConfig() + `
resource "vew_component" "test" {
  project_id = "prog-73488"
  name = "terraform-test-component"
  description = "created by protocol test"
  platform = "Linux"
  supported_architectures = ["arm64"]
  supported_os_versions = ["Ubuntu 24"]
}`}}})
}
