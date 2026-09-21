package provider

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/elva-labs/terraform-provider-vew/internal/client"
	testresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const testAccComponentResourceName = "vew_component.test"

func TestAccComponentResource(t *testing.T) {
	projectID := os.Getenv("VEW_PROJECT_ID")
	name := fmt.Sprintf("tf-acc-%d", time.Now().UTC().UnixNano())
	initialDescription := "created by Terraform acceptance test"
	updatedDescription := "updated by Terraform acceptance test"

	testresource.Test(t, testresource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories(),
		PreCheck: func() {
			testAccPreCheck(t)
		},
		CheckDestroy: testAccCheckComponentDestroy(projectID),
		Steps: []testresource.TestStep{
			{
				Config: testAccComponentResourceConfig(projectID, name, initialDescription),
				Check: testresource.ComposeTestCheckFunc(
					testresource.TestCheckResourceAttrSet(testAccComponentResourceName, "id"),
					testresource.TestCheckResourceAttr(testAccComponentResourceName, "name", name),
					testresource.TestCheckResourceAttr(testAccComponentResourceName, "description", initialDescription),
					testresource.TestCheckResourceAttr(testAccComponentResourceName, "status", "CREATED"),
				),
			},
			{
				Config:   testAccComponentResourceConfig(projectID, name, initialDescription),
				PlanOnly: true,
			},
			{
				Config: testAccComponentResourceConfig(projectID, name, updatedDescription),
				Check: testresource.ComposeTestCheckFunc(
					testresource.TestCheckResourceAttr(testAccComponentResourceName, "name", name),
					testresource.TestCheckResourceAttr(testAccComponentResourceName, "description", updatedDescription),
				),
			},
			{
				ResourceName:      testAccComponentResourceName,
				ImportState:       true,
				ImportStateIdFunc: testAccComponentImportID(projectID),
				ImportStateVerify: true,
			},
		},
	})
}

func testAccPreCheck(t *testing.T) {
	t.Helper()

	var missing []string
	for _, name := range []string{
		"VEW_API_URL",
		"VEW_TOKEN_URL",
		"VEW_CLIENT_ID",
		"VEW_CLIENT_SECRET",
		"VEW_PROJECT_ID",
	} {
		if strings.TrimSpace(os.Getenv(name)) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("VEW acceptance test requires environment variables: %s", strings.Join(missing, ", "))
	}
}

func testAccComponentResourceConfig(projectID, name, description string) string {
	return fmt.Sprintf(`
resource "vew_component" "test" {
  project_id              = %q
  name                    = %q
  description             = %q
  platform                = "Linux"
  supported_architectures = ["arm64", "x86_64"]
  supported_os_versions   = ["Ubuntu 24"]
}
`, projectID, name, description)
}

func testAccComponentImportID(projectID string) testresource.ImportStateIdFunc {
	return func(state *terraform.State) (string, error) {
		resource, ok := state.RootModule().Resources[testAccComponentResourceName]
		if !ok || resource.Primary == nil || resource.Primary.ID == "" {
			return "", fmt.Errorf("component state is missing an ID")
		}
		return projectID + "/" + resource.Primary.ID, nil
	}
}

func testAccCheckComponentDestroy(projectID string) testresource.TestCheckFunc {
	return func(state *terraform.State) error {
		api, err := client.New(client.Config{
			APIURL:       os.Getenv("VEW_API_URL"),
			TokenURL:     os.Getenv("VEW_TOKEN_URL"),
			ClientID:     os.Getenv("VEW_CLIENT_ID"),
			ClientSecret: os.Getenv("VEW_CLIENT_SECRET"),
		})
		if err != nil {
			return fmt.Errorf("create VEW client for destroy check: %w", err)
		}

		for _, resource := range state.RootModule().Resources {
			if resource.Type != "vew_component" || resource.Primary == nil || resource.Primary.ID == "" {
				continue
			}
			component, err := api.GetComponent(context.Background(), projectID, resource.Primary.ID)
			if client.IsNotFound(err) {
				continue
			}
			if err != nil {
				return fmt.Errorf("read destroyed VEW component %q: %w", resource.Primary.ID, err)
			}
			if !strings.EqualFold(component.Status, "ARCHIVED") {
				return fmt.Errorf("VEW component %q status = %q, want ARCHIVED or 404", resource.Primary.ID, component.Status)
			}
		}
		return nil
	}
}
