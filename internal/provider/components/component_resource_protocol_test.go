package components_test

import (
	"fmt"
	"testing"

	rootprovider "github.com/elva-labs/terraform-provider-vew/internal/provider"
	"github.com/elva-labs/terraform-provider-vew/internal/provider/testhelpers"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	testresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// The root-provider factory stays here rather than testhelpers so helpers
// never import the root provider and therefore cannot form an import cycle.
func testProtoV6ProviderFactories() map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){"vew": providerserver.NewProtocol6WithError(rootprovider.New("test")())}
}

func TestComponentResourceLifecycle(t *testing.T) {
	fake := testhelpers.NewVEWServer(t)
	config := fake.ProviderConfig() + componentResourceConfig("created by provider test")
	testresource.Test(t, testresource.TestCase{
		IsUnitTest: true, ProtoV6ProviderFactories: testProtoV6ProviderFactories(),
		CheckDestroy: func(*terraform.State) error {
			snapshot := fake.Snapshot()
			if !snapshot.Archived || snapshot.ArchiveCalls != 1 {
				return fmt.Errorf("archive state/calls = %t/%d, want true/1", snapshot.Archived, snapshot.ArchiveCalls)
			}
			return nil
		},
		Steps: []testresource.TestStep{
			{Config: config, Check: testresource.ComposeTestCheckFunc(testresource.TestCheckResourceAttr("vew_component.test", "id", "cmp-123"), testresource.TestCheckResourceAttr("vew_component.test", "status", "CREATED"), testresource.TestCheckResourceAttr("vew_component.test", "created_by", "terraform-test-user"), func(*terraform.State) error {
				snapshot := fake.Snapshot()
				if snapshot.CreateCalls != 1 || snapshot.GetCalls != 1 || snapshot.IdempotencyKey == "" || snapshot.Create.Name != "terraform-test-component" || snapshot.Create.Description != "created by provider test" || snapshot.Create.Platform != "Linux" || len(snapshot.Create.SupportedArchitectures) != 2 || snapshot.Create.SupportedArchitectures[0] != "arm64" || snapshot.Create.SupportedArchitectures[1] != "x86_64" || len(snapshot.Create.SupportedOSVersions) != 1 || snapshot.Create.SupportedOSVersions[0] != "Ubuntu 24" {
					return fmt.Errorf("create/get/key/input = %#v", snapshot)
				}
				return nil
			})},
			{Config: config, PlanOnly: true, Check: func(*terraform.State) error {
				snapshot := fake.Snapshot()
				if snapshot.CreateCalls != 1 || snapshot.UpdateCalls != 0 {
					return fmt.Errorf("create/update calls = %d/%d, want 1/0", snapshot.CreateCalls, snapshot.UpdateCalls)
				}
				return nil
			}},
			{Config: fake.ProviderConfig() + componentResourceConfig("updated by provider test"), Check: testresource.ComposeTestCheckFunc(testresource.TestCheckResourceAttr("vew_component.test", "description", "updated by provider test"), testresource.TestCheckResourceAttr("vew_component.test", "updated_by", "terraform-test-updater"), func(*terraform.State) error {
				snapshot := fake.Snapshot()
				if snapshot.UpdateCalls != 1 || snapshot.Update.Description != "updated by provider test" || snapshot.GetCalls < 2 {
					return fmt.Errorf("update/input/get = %#v", snapshot)
				}
				return nil
			})},
			{ResourceName: "vew_component.test", ImportState: true, ImportStateId: "project-example/cmp-123", ImportStateVerify: true},
		},
	})
}

func componentResourceConfig(description string) string {
	return fmt.Sprintf(`
resource "vew_component" "test" {
  project_id              = "project-example"
  name                    = "terraform-test-component"
  description             = %q
  platform                = "Linux"
  supported_architectures = ["arm64", "x86_64"]
  supported_os_versions   = ["Ubuntu 24"]
}
`, description)
}
