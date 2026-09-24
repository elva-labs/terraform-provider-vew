package pipelines_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	rootprovider "github.com/elva-labs/terraform-provider-vew/internal/provider"
	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	vewpipelines "github.com/elva-labs/terraform-provider-vew/internal/vew/pipelines"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	testresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const accPipelineAddress = "vew_pipeline.disposable"

func pipelineAcceptanceMissingGates() []string {
	var missing []string
	for _, name := range []string{"TF_ACC", "VEW_ACC_PIPELINE", "VEW_API_URL", "VEW_TOKEN_URL", "VEW_CLIENT_ID", "VEW_CLIENT_SECRET", "VEW_TEST_PROJECT_ID", "VEW_TEST_RECIPE_ID", "VEW_TEST_RECIPE_VERSION_ID"} {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" || ((name == "TF_ACC" || name == "VEW_ACC_PIPELINE") && value != "1") {
			missing = append(missing, name)
		}
	}
	return missing
}

func TestAccPipelineResource(t *testing.T) {
	if missing := pipelineAcceptanceMissingGates(); len(missing) > 0 {
		t.Skipf("pipeline acceptance test requires: %s", strings.Join(missing, ", "))
	}
	projectID := strings.TrimSpace(os.Getenv("VEW_TEST_PROJECT_ID"))
	recipeID := strings.TrimSpace(os.Getenv("VEW_TEST_RECIPE_ID"))
	versionID := strings.TrimSpace(os.Getenv("VEW_TEST_RECIPE_VERSION_ID"))
	name := fmt.Sprintf("tf-acc-pipeline-%d", time.Now().UTC().UnixNano())
	testresource.Test(t, testresource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){"vew": providerserver.NewProtocol6WithError(rootprovider.New("test")())},
		PreCheck: func() {
			if missing := pipelineAcceptanceMissingGates(); len(missing) > 0 {
				t.Fatalf("pipeline acceptance test requires: %s", strings.Join(missing, ", "))
			}
		},
		CheckDestroy: func(state *terraform.State) error {
			transport, err := vew.NewTransportWithScopes(vew.Config{APIURL: os.Getenv("VEW_API_URL"), TokenURL: os.Getenv("VEW_TOKEN_URL"), ClientID: os.Getenv("VEW_CLIENT_ID"), ClientSecret: os.Getenv("VEW_CLIENT_SECRET")}, "clients/packaging/pipeline.read", "clients/packaging/pipeline.write")
			if err != nil {
				return err
			}
			api := vewpipelines.NewClient(transport)
			for _, resource := range state.RootModule().Resources {
				if resource.Type != "vew_pipeline" || resource.Primary == nil || resource.Primary.ID == "" {
					continue
				}
				remote, err := api.GetPipeline(context.Background(), projectID, resource.Primary.ID)
				if err != nil && !vew.IsNotFound(err) {
					return err
				}
				if err == nil && remote.Status != "RETIRED" {
					return fmt.Errorf("disposable pipeline %s status = %s, want RETIRED", resource.Primary.ID, remote.Status)
				}
			}
			return nil
		},
		Steps: []testresource.TestStep{
			{Config: pipelineAcceptanceConfig(projectID, recipeID, versionID, name, "0 0 * * ? *"), Check: testresource.ComposeTestCheckFunc(testresource.TestCheckResourceAttr(accPipelineAddress, "status", "CREATED"), testresource.TestCheckResourceAttrSet(accPipelineAddress, "id"))},
			{Config: pipelineAcceptanceConfig(projectID, recipeID, versionID, name, "0 6 * * ? *"), Check: testresource.TestCheckResourceAttr(accPipelineAddress, "schedule", "0 6 * * ? *")},
			{ResourceName: accPipelineAddress, ImportState: true, ImportStateIdFunc: func(state *terraform.State) (string, error) {
				resource := state.RootModule().Resources[accPipelineAddress]
				if resource == nil || resource.Primary == nil || resource.Primary.ID == "" {
					return "", fmt.Errorf("pipeline state missing ID")
				}
				return projectID + "/" + resource.Primary.ID, nil
			}, ImportStateVerify: true},
		},
	})
}

func pipelineAcceptanceConfig(projectID, recipeID, versionID, name, schedule string) string {
	return fmt.Sprintf(`resource "vew_pipeline" "disposable" {
  project_id = %q
  name = %q
  description = "Disposable Terraform pipeline acceptance test"
  recipe_id = %q
  recipe_version_id = %q
  build_instance_types = ["m8i.2xlarge"]
  schedule = %q
}
`, projectID, name, recipeID, versionID, schedule)
}
