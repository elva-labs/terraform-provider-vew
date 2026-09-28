package technologies_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	rootprovider "github.com/elva-labs/terraform-provider-vew/internal/provider"
	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	vewtechnologies "github.com/elva-labs/terraform-provider-vew/internal/vew/technologies"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	testresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const accTechnologyAddress = "vew_technology.disposable"

func technologyAcceptanceMissingGates(getenv func(string) string) []string {
	var missing []string
	for _, name := range []string{
		"TF_ACC", "VEW_ACC_TECHNOLOGY", "VEW_API_URL", "VEW_PROJECTS_API_URL",
		"VEW_TOKEN_URL", "VEW_CLIENT_ID", "VEW_CLIENT_SECRET", "VEW_TEST_PROJECT_ID",
	} {
		value := strings.TrimSpace(getenv(name))
		if value == "" || ((name == "TF_ACC" || name == "VEW_ACC_TECHNOLOGY") && value != "1") {
			missing = append(missing, name)
		}
	}
	return missing
}

func TestTechnologyAcceptanceGate(t *testing.T) {
	missing := technologyAcceptanceMissingGates(func(string) string { return "" })
	want := []string{"TF_ACC", "VEW_ACC_TECHNOLOGY", "VEW_API_URL", "VEW_PROJECTS_API_URL", "VEW_TOKEN_URL", "VEW_CLIENT_ID", "VEW_CLIENT_SECRET", "VEW_TEST_PROJECT_ID"}
	if strings.Join(missing, ",") != strings.Join(want, ",") {
		t.Fatalf("missing gates = %v, want %v", missing, want)
	}
}

func TestAccTechnologyResourceLiveDisposable(t *testing.T) {
	if missing := technologyAcceptanceMissingGates(os.Getenv); len(missing) > 0 {
		t.Skipf("technology live acceptance requires: %s; it creates and deletes a disposable VEW technology", strings.Join(missing, ", "))
	}
	projectID := strings.TrimSpace(os.Getenv("VEW_TEST_PROJECT_ID"))
	name := fmt.Sprintf("tf-acc-technology-%d", time.Now().UTC().UnixNano())
	initialDescription := "disposable Terraform technology acceptance fixture"
	updatedDescription := initialDescription + " updated"
	testresource.Test(t, testresource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){"vew": providerserver.NewProtocol6WithError(rootprovider.New("test")())},
		PreCheck: func() {
			if missing := technologyAcceptanceMissingGates(os.Getenv); len(missing) > 0 {
				t.Fatalf("technology live acceptance requires: %s", strings.Join(missing, ", "))
			}
		},
		CheckDestroy: func(state *terraform.State) error {
			config := vew.Config{APIURL: os.Getenv("VEW_PROJECTS_API_URL"), TokenURL: os.Getenv("VEW_TOKEN_URL"), ClientID: os.Getenv("VEW_CLIENT_ID"), ClientSecret: os.Getenv("VEW_CLIENT_SECRET")}
			write, err := vew.NewTransportWithScopes(config, "clients/projects/technology.write")
			if err != nil {
				return err
			}
			read, err := vew.NewTransportWithScopes(config, "clients/projects/technology.read")
			if err != nil {
				return err
			}
			api := vewtechnologies.NewClient(write, read)
			for _, resource := range state.RootModule().Resources {
				if resource.Type != "vew_technology" || resource.Primary == nil || resource.Primary.ID == "" {
					continue
				}
				_, err := api.GetTechnology(context.Background(), projectID, resource.Primary.ID)
				if err == nil {
					return fmt.Errorf("disposable technology %q still exists after Terraform delete", resource.Primary.ID)
				}
				if !vew.IsNotFound(err) {
					return fmt.Errorf("check deleted disposable technology %q: %w", resource.Primary.ID, err)
				}
			}
			return nil
		},
		Steps: []testresource.TestStep{
			{Config: technologyAcceptanceConfig(projectID, name, initialDescription), Check: testresource.ComposeTestCheckFunc(testresource.TestCheckResourceAttrSet(accTechnologyAddress, "id"), testresource.TestCheckResourceAttr(accTechnologyAddress, "name", name), testresource.TestCheckResourceAttr(accTechnologyAddress, "description", initialDescription))},
			{Config: technologyAcceptanceConfig(projectID, name, updatedDescription), Check: testresource.TestCheckResourceAttr(accTechnologyAddress, "description", updatedDescription)},
			{ResourceName: accTechnologyAddress, ImportState: true, ImportStateIdFunc: technologyAcceptanceImportID(projectID), ImportStateVerify: true},
		},
	})
}

func technologyAcceptanceConfig(projectID, name, description string) string {
	return fmt.Sprintf(`resource "vew_technology" "disposable" {
  project_id  = %q
  name        = %q
  description = %q
}
`, projectID, name, description)
}

func technologyAcceptanceImportID(projectID string) testresource.ImportStateIdFunc {
	return func(state *terraform.State) (string, error) {
		resource := state.RootModule().Resources[accTechnologyAddress]
		if resource == nil || resource.Primary == nil || resource.Primary.ID == "" {
			return "", fmt.Errorf("disposable technology state is missing an ID")
		}
		return projectID + "/" + resource.Primary.ID, nil
	}
}
