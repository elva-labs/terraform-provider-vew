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

const (
	testAccComponentVersionResourceName = "vew_component_version.test"
	testAccDisposableComponentName      = "vew_component.disposable"
)

var componentVersionAcceptanceGateNames = []string{
	"TF_ACC",
	"VEW_ACC_COMPONENT_VERSION",
	"VEW_API_URL",
	"VEW_TOKEN_URL",
	"VEW_CLIENT_ID",
	"VEW_CLIENT_SECRET",
	"VEW_TEST_PROJECT_ID",
}

func TestComponentVersionAcceptanceMissingGates(t *testing.T) {
	got := componentVersionAcceptanceMissingGates(func(name string) string {
		if name == "TF_ACC" {
			return "1"
		}
		if name == "VEW_TEST_PROJECT_ID" {
			return "set"
		}
		return ""
	})
	want := []string{
		"VEW_ACC_COMPONENT_VERSION",
		"VEW_API_URL",
		"VEW_TOKEN_URL",
		"VEW_CLIENT_ID",
		"VEW_CLIENT_SECRET",
	}
	if len(got) != len(want) {
		t.Fatalf("missing gates = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("missing gates = %v, want %v", got, want)
		}
	}
}

func TestComponentVersionAcceptanceRequiresExplicitOptInValues(t *testing.T) {
	got := componentVersionAcceptanceMissingGates(func(name string) string {
		if name == "TF_ACC" || name == "VEW_ACC_COMPONENT_VERSION" {
			return "true"
		}
		return "set"
	})
	want := []string{"TF_ACC", "VEW_ACC_COMPONENT_VERSION"}
	if len(got) != len(want) {
		t.Fatalf("missing gates = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("missing gates = %v, want %v", got, want)
		}
	}
}

func componentVersionAcceptanceMissingGates(getenv func(string) string) []string {
	var missing []string
	for _, name := range componentVersionAcceptanceGateNames {
		value := strings.TrimSpace(getenv(name))
		if (name == "TF_ACC" || name == "VEW_ACC_COMPONENT_VERSION") && value != "1" {
			missing = append(missing, name)
			continue
		}
		if value == "" {
			missing = append(missing, name)
		}
	}
	return missing
}

func TestAccComponentVersionResource(t *testing.T) {
	if missing := componentVersionAcceptanceMissingGates(os.Getenv); len(missing) > 0 {
		t.Skipf("component-version acceptance test requires: %s", strings.Join(missing, ", "))
	}

	projectID := strings.TrimSpace(os.Getenv("VEW_TEST_PROJECT_ID"))
	componentName := fmt.Sprintf("tf-acc-component-version-%d", time.Now().UTC().UnixNano())
	initialDescription := "created by Terraform component-version acceptance test"
	updatedDescription := "updated by Terraform component-version acceptance test"
	initialNotes := "initial acceptance-test notes"
	updatedNotes := "updated acceptance-test notes"
	initialConfig := testAccComponentVersionConfig(projectID, componentName, initialDescription, initialNotes)

	testresource.Test(t, testresource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories(),
		PreCheck:                 func() { testAccComponentVersionPreCheck(t) },
		CheckDestroy:             testAccCheckComponentVersionDestroy(projectID),
		Steps: []testresource.TestStep{
			{
				Config: initialConfig,
				Check: testresource.ComposeTestCheckFunc(
					testresource.TestCheckResourceAttrSet(testAccDisposableComponentName, "id"),
					testresource.TestCheckResourceAttrSet(testAccComponentVersionResourceName, "id"),
					testresource.TestCheckResourceAttr(testAccComponentVersionResourceName, "status", "VALIDATED"),
					testresource.TestCheckResourceAttr(testAccComponentVersionResourceName, "description", initialDescription),
					testresource.TestCheckResourceAttr(testAccComponentVersionResourceName, "notes", initialNotes),
				),
			},
			{Config: initialConfig, PlanOnly: true},
			{
				Config: testAccComponentVersionConfig(projectID, componentName, updatedDescription, updatedNotes),
				Check: testresource.ComposeTestCheckFunc(
					testresource.TestCheckResourceAttr(testAccComponentVersionResourceName, "status", "VALIDATED"),
					testresource.TestCheckResourceAttr(testAccComponentVersionResourceName, "description", updatedDescription),
					testresource.TestCheckResourceAttr(testAccComponentVersionResourceName, "notes", updatedNotes),
				),
			},
			{
				ResourceName:            testAccComponentVersionResourceName,
				ImportState:             true,
				ImportStateIdFunc:       testAccComponentVersionImportID(projectID),
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"release_type"},
			},
		},
	})
}

func testAccComponentVersionPreCheck(t *testing.T) {
	t.Helper()
	if missing := componentVersionAcceptanceMissingGates(os.Getenv); len(missing) > 0 {
		t.Fatalf("component-version acceptance test requires environment variables: %s", strings.Join(missing, ", "))
	}
}

func testAccComponentVersionConfig(projectID, componentName, description, notes string) string {
	return fmt.Sprintf(`
resource "vew_component" "disposable" {
  project_id              = %q
  name                    = %q
  description             = "Disposable parent for Terraform component-version acceptance testing"
  platform                = "Linux"
  supported_architectures = ["arm64", "x86_64"]
  supported_os_versions   = ["Ubuntu 24"]
}

resource "vew_component_version" "test" {
  project_id        = vew_component.disposable.project_id
  component_id      = vew_component.disposable.id
  description       = %q
  release_type      = "PATCH"
  definition_json   = jsonencode({ phases = [] })
  dependencies      = []
  software_vendor   = "Terraform acceptance test"
  software_version  = "1.0.0"
  notes             = %q
}
`, projectID, componentName, description, notes)
}

func testAccComponentVersionImportID(projectID string) testresource.ImportStateIdFunc {
	return func(state *terraform.State) (string, error) {
		resource, ok := state.RootModule().Resources[testAccComponentVersionResourceName]
		if !ok || resource.Primary == nil || resource.Primary.ID == "" {
			return "", fmt.Errorf("component version state is missing an ID")
		}
		componentID := resource.Primary.Attributes["component_id"]
		if componentID == "" {
			return "", fmt.Errorf("component version state is missing component_id")
		}
		return projectID + "/" + componentID + "/" + resource.Primary.ID, nil
	}
}

func testAccCheckComponentVersionDestroy(projectID string) testresource.TestCheckFunc {
	return func(state *terraform.State) error {
		transport, err := vew.NewTransport(vew.Config{APIURL: os.Getenv("VEW_API_URL"), TokenURL: os.Getenv("VEW_TOKEN_URL"), ClientID: os.Getenv("VEW_CLIENT_ID"), ClientSecret: os.Getenv("VEW_CLIENT_SECRET")})
		if err != nil {
			return err
		}
		api := components.NewClient(transport)
		componentIDs := map[string]struct{}{}
		for _, resource := range state.RootModule().Resources {
			if resource.Primary == nil || resource.Primary.ID == "" {
				continue
			}
			switch resource.Type {
			case "vew_component_version":
				componentID := resource.Primary.Attributes["component_id"]
				if componentID == "" {
					return fmt.Errorf("component version %q is missing component_id", resource.Primary.ID)
				}
				componentIDs[componentID] = struct{}{}
				version, err := api.GetComponentVersion(context.Background(), projectID, componentID, resource.Primary.ID)
				if err != nil {
					return fmt.Errorf("read disposable component version %q: %w", resource.Primary.ID, err)
				}
				if !strings.EqualFold(version.Status, "RETIRED") {
					return fmt.Errorf("disposable component version %q status = %q, want RETIRED", resource.Primary.ID, version.Status)
				}
			case "vew_component":
				componentIDs[resource.Primary.ID] = struct{}{}
			}
		}
		for componentID := range componentIDs {
			component, err := api.GetComponent(context.Background(), projectID, componentID)
			if err != nil {
				return fmt.Errorf("read disposable component %q: %w", componentID, err)
			}
			if !strings.EqualFold(component.Status, "ARCHIVED") {
				if err := api.ArchiveComponent(context.Background(), projectID, componentID); err != nil {
					return fmt.Errorf("archive disposable component %q: %w", componentID, err)
				}
				component, err = api.GetComponent(context.Background(), projectID, componentID)
				if err != nil {
					return fmt.Errorf("read archived disposable component %q: %w", componentID, err)
				}
			}
			if !strings.EqualFold(component.Status, "ARCHIVED") {
				return fmt.Errorf("disposable component %q status = %q, want ARCHIVED", componentID, component.Status)
			}
		}
		return nil
	}
}
