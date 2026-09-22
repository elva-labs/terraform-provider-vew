package components_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	"github.com/elva-labs/terraform-provider-vew/internal/vew/components"
	testresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const testAccComponentResourceName = "vew_component.test"

func TestAccComponentResource(t *testing.T) {
	projectID := os.Getenv("VEW_PROJECT_ID")
	name := fmt.Sprintf("tf-acc-%d", time.Now().UTC().UnixNano())
	testresource.Test(t, testresource.TestCase{ProtoV6ProviderFactories: testProtoV6ProviderFactories(), PreCheck: func() { testAccPreCheck(t) }, CheckDestroy: testAccCheckComponentDestroy(projectID), Steps: []testresource.TestStep{{Config: testAccComponentResourceConfig(projectID, name, "created by Terraform acceptance test")}}})
}

func testAccPreCheck(t *testing.T) {
	t.Helper()
	var missing []string
	for _, name := range []string{"VEW_API_URL", "VEW_TOKEN_URL", "VEW_CLIENT_ID", "VEW_CLIENT_SECRET", "VEW_PROJECT_ID"} {
		if strings.TrimSpace(os.Getenv(name)) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("VEW acceptance test requires environment variables: %s", strings.Join(missing, ", "))
	}
}
func testAccComponentResourceConfig(projectID, name, description string) string {
	return fmt.Sprintf("resource \\\"vew_component\\\" \\\"test\\\" { project_id=%q name=%q description=%q platform=\\\"Linux\\\" supported_architectures=[\\\"arm64\\\"] supported_os_versions=[\\\"Ubuntu 24\\\"] }", projectID, name, description)
}
func testAccCheckComponentDestroy(projectID string) testresource.TestCheckFunc {
	return func(state *terraform.State) error {
		transport, err := vew.NewTransport(vew.Config{APIURL: os.Getenv("VEW_API_URL"), TokenURL: os.Getenv("VEW_TOKEN_URL"), ClientID: os.Getenv("VEW_CLIENT_ID"), ClientSecret: os.Getenv("VEW_CLIENT_SECRET")})
		if err != nil {
			return err
		}
		api := components.NewClient(transport)
		for _, resource := range state.RootModule().Resources {
			if resource.Type == "vew_component" && resource.Primary != nil && resource.Primary.ID != "" {
				component, err := api.GetComponent(context.Background(), projectID, resource.Primary.ID)
				if err != nil && !vew.IsNotFound(err) {
					return err
				}
				if err == nil && !strings.EqualFold(component.Status, "ARCHIVED") {
					return fmt.Errorf("VEW component %q status = %q", resource.Primary.ID, component.Status)
				}
			}
		}
		return nil
	}
}
