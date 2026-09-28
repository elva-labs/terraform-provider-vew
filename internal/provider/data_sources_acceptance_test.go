package provider_test

import (
	"fmt"
	"os"
	"strings"
	"testing"

	rootprovider "github.com/elva-labs/terraform-provider-vew/internal/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	testresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func dataSourcesAcceptanceMissingGates() []string {
	var missing []string
	for _, name := range []string{
		"TF_ACC", "VEW_ACC_DATA_SOURCES", "VEW_API_URL", "VEW_TOKEN_URL", "VEW_CLIENT_ID", "VEW_CLIENT_SECRET",
		"VEW_TEST_PROJECT_ID", "VEW_TEST_COMPONENT_ID", "VEW_TEST_COMPONENT_VERSION_ID",
		"VEW_TEST_RECIPE_ID", "VEW_TEST_RECIPE_VERSION_ID", "VEW_TEST_PIPELINE_ID", "VEW_TEST_IMAGE_ID",
	} {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" || ((name == "TF_ACC" || name == "VEW_ACC_DATA_SOURCES") && value != "1") {
			missing = append(missing, name)
		}
	}
	return missing
}

func TestAccDataSourcesReadExistingObjectsOnly(t *testing.T) {
	if missing := dataSourcesAcceptanceMissingGates(); len(missing) > 0 {
		t.Skipf("read-only data-source acceptance test requires: %s", strings.Join(missing, ", "))
	}

	config := dataSourcesAcceptanceConfig()
	testresource.Test(t, testresource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"vew": providerserver.NewProtocol6WithError(rootprovider.New("test")()),
		},
		PreCheck: func() {
			if missing := dataSourcesAcceptanceMissingGates(); len(missing) > 0 {
				t.Fatalf("read-only data-source acceptance test requires: %s", strings.Join(missing, ", "))
			}
		},
		Steps: []testresource.TestStep{{
			Config: config,
			Check: testresource.ComposeTestCheckFunc(
				testresource.TestCheckResourceAttr("data.vew_component.existing", "component_id", strings.TrimSpace(os.Getenv("VEW_TEST_COMPONENT_ID"))),
				testresource.TestCheckResourceAttrSet("data.vew_component.existing", "status"),
				testresource.TestCheckResourceAttr("data.vew_component_version.existing", "component_id", strings.TrimSpace(os.Getenv("VEW_TEST_COMPONENT_ID"))),
				testresource.TestCheckResourceAttr("data.vew_component_version.existing", "version_id", strings.TrimSpace(os.Getenv("VEW_TEST_COMPONENT_VERSION_ID"))),
				testresource.TestCheckResourceAttrSet("data.vew_component_version.existing", "status"),
				testresource.TestCheckResourceAttr("data.vew_recipe.existing", "recipe_id", strings.TrimSpace(os.Getenv("VEW_TEST_RECIPE_ID"))),
				testresource.TestCheckResourceAttrSet("data.vew_recipe.existing", "status"),
				testresource.TestCheckResourceAttr("data.vew_recipe_version.existing", "recipe_id", strings.TrimSpace(os.Getenv("VEW_TEST_RECIPE_ID"))),
				testresource.TestCheckResourceAttr("data.vew_recipe_version.existing", "version_id", strings.TrimSpace(os.Getenv("VEW_TEST_RECIPE_VERSION_ID"))),
				testresource.TestCheckResourceAttrSet("data.vew_recipe_version.existing", "status"),
				testresource.TestCheckResourceAttr("data.vew_pipeline.existing", "pipeline_id", strings.TrimSpace(os.Getenv("VEW_TEST_PIPELINE_ID"))),
				testresource.TestCheckResourceAttrSet("data.vew_pipeline.existing", "status"),
				testresource.TestCheckResourceAttr("data.vew_image.existing", "image_id", strings.TrimSpace(os.Getenv("VEW_TEST_IMAGE_ID"))),
				testresource.TestCheckResourceAttrSet("data.vew_image.existing", "status"),
			),
		}},
	})
}

func dataSourcesAcceptanceConfig() string {
	return fmt.Sprintf(`provider "vew" {
  api_url       = %q
  token_url     = %q
  client_id     = %q
  client_secret = %q
}

data "vew_component" "existing" {
  project_id   = %q
  component_id = %q
}

data "vew_component_version" "existing" {
  project_id   = %q
  component_id = %q
  version_id   = %q
}

data "vew_recipe" "existing" {
  project_id = %q
  recipe_id  = %q
}

data "vew_recipe_version" "existing" {
  project_id = %q
  recipe_id  = %q
  version_id = %q
}

data "vew_pipeline" "existing" {
  project_id  = %q
  pipeline_id = %q
}

data "vew_image" "existing" {
  project_id = %q
  image_id   = %q
}
`,
		os.Getenv("VEW_API_URL"), os.Getenv("VEW_TOKEN_URL"), os.Getenv("VEW_CLIENT_ID"), os.Getenv("VEW_CLIENT_SECRET"),
		os.Getenv("VEW_TEST_PROJECT_ID"), os.Getenv("VEW_TEST_COMPONENT_ID"),
		os.Getenv("VEW_TEST_PROJECT_ID"), os.Getenv("VEW_TEST_COMPONENT_ID"), os.Getenv("VEW_TEST_COMPONENT_VERSION_ID"),
		os.Getenv("VEW_TEST_PROJECT_ID"), os.Getenv("VEW_TEST_RECIPE_ID"),
		os.Getenv("VEW_TEST_PROJECT_ID"), os.Getenv("VEW_TEST_RECIPE_ID"), os.Getenv("VEW_TEST_RECIPE_VERSION_ID"),
		os.Getenv("VEW_TEST_PROJECT_ID"), os.Getenv("VEW_TEST_PIPELINE_ID"),
		os.Getenv("VEW_TEST_PROJECT_ID"), os.Getenv("VEW_TEST_IMAGE_ID"),
	)
}
