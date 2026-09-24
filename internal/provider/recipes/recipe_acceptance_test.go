package recipes_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	rootprovider "github.com/elva-labs/terraform-provider-vew/internal/provider"
	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	vewrecipes "github.com/elva-labs/terraform-provider-vew/internal/vew/recipes"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	testresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const (
	accRecipeResourceName        = "vew_recipe.disposable"
	accRecipeVersionResourceName = "vew_recipe_version.disposable"
)

func recipeAcceptanceMissingGates() []string {
	var missing []string
	for _, name := range []string{
		"TF_ACC", "VEW_ACC_RECIPE", "VEW_API_URL", "VEW_TOKEN_URL",
		"VEW_CLIENT_ID", "VEW_CLIENT_SECRET", "VEW_TEST_PROJECT_ID",
	} {
		value := strings.TrimSpace(os.Getenv(name))
		if (name == "TF_ACC" || name == "VEW_ACC_RECIPE") && value != "1" || value == "" {
			missing = append(missing, name)
		}
	}
	return missing
}

func TestAccRecipeResource(t *testing.T) {
	if missing := recipeAcceptanceMissingGates(); len(missing) > 0 {
		t.Skipf("recipe acceptance test requires: %s", strings.Join(missing, ", "))
	}
	projectID := strings.TrimSpace(os.Getenv("VEW_TEST_PROJECT_ID"))
	name := fmt.Sprintf("tf-acc-recipe-%d", time.Now().UTC().UnixNano())
	initialDescription := "created by Terraform recipe acceptance test"
	updatedDescription := "updated by Terraform recipe acceptance test"

	testresource.Test(t, testresource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"vew": providerserver.NewProtocol6WithError(rootprovider.New("test")()),
		},
		PreCheck: func() {
			if missing := recipeAcceptanceMissingGates(); len(missing) > 0 {
				t.Fatalf("recipe acceptance test requires: %s", strings.Join(missing, ", "))
			}
		},
		CheckDestroy: accCheckRecipeDestroy(projectID),
		Steps: []testresource.TestStep{
			{
				Config: accRecipeConfig(projectID, name, initialDescription),
				Check: testresource.ComposeTestCheckFunc(
					testresource.TestCheckResourceAttr(accRecipeResourceName, "name", name),
					testresource.TestCheckResourceAttr(accRecipeResourceName, "status", "CREATED"),
					testresource.TestCheckResourceAttrSet(accRecipeResourceName, "id"),
					testresource.TestCheckResourceAttr(accRecipeVersionResourceName, "description", initialDescription),
					testresource.TestCheckResourceAttr(accRecipeVersionResourceName, "status", "VALIDATED"),
					testresource.TestCheckResourceAttrSet(accRecipeVersionResourceName, "id"),
				),
			},
			{
				Config: accRecipeConfig(projectID, name, updatedDescription),
				Check: testresource.ComposeTestCheckFunc(
					testresource.TestCheckResourceAttr(accRecipeVersionResourceName, "description", updatedDescription),
					testresource.TestCheckResourceAttr(accRecipeVersionResourceName, "status", "VALIDATED"),
				),
			},
			{
				ResourceName:      accRecipeResourceName,
				ImportState:       true,
				ImportStateIdFunc: accRecipeImportID(projectID),
				ImportStateVerify: true,
			},
			{
				ResourceName:            accRecipeVersionResourceName,
				ImportState:             true,
				ImportStateIdFunc:       accRecipeVersionImportID(projectID),
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"release_type", "configured_components"},
			},
		},
	})
}

func accRecipeConfig(projectID, name, versionDescription string) string {
	return fmt.Sprintf(`
resource "vew_recipe" "disposable" {
  project_id   = %q
  name         = %q
  description  = "Disposable parent for Terraform recipe acceptance testing"
  platform     = "Linux"
  architecture = "amd64"
  os_version   = "Ubuntu 24"
}

resource "vew_recipe_version" "disposable" {
  project_id            = vew_recipe.disposable.project_id
  recipe_id             = vew_recipe.disposable.id
  description           = %q
  release_type          = "PATCH"
  volume_size           = 20
  configured_components = []
}
`, projectID, name, versionDescription)
}

func accRecipeImportID(projectID string) testresource.ImportStateIdFunc {
	return func(state *terraform.State) (string, error) {
		recipe, err := accRecipeState(state, accRecipeResourceName)
		if err != nil {
			return "", err
		}
		return projectID + "/" + recipe.Primary.ID, nil
	}
}

func accRecipeVersionImportID(projectID string) testresource.ImportStateIdFunc {
	return func(state *terraform.State) (string, error) {
		version, err := accRecipeState(state, accRecipeVersionResourceName)
		if err != nil {
			return "", err
		}
		recipeID := version.Primary.Attributes["recipe_id"]
		if recipeID == "" {
			return "", fmt.Errorf("disposable recipe version state is missing recipe_id")
		}
		return projectID + "/" + recipeID + "/" + version.Primary.ID, nil
	}
}

func accRecipeState(state *terraform.State, name string) (*terraform.ResourceState, error) {
	resource, ok := state.RootModule().Resources[name]
	if !ok || resource.Primary == nil || resource.Primary.ID == "" {
		return nil, fmt.Errorf("%s state is missing an ID", name)
	}
	return resource, nil
}

func accCheckRecipeDestroy(projectID string) testresource.TestCheckFunc {
	return func(state *terraform.State) error {
		transport, err := vew.NewTransportWithScopes(vew.Config{
			APIURL: os.Getenv("VEW_API_URL"), TokenURL: os.Getenv("VEW_TOKEN_URL"),
			ClientID: os.Getenv("VEW_CLIENT_ID"), ClientSecret: os.Getenv("VEW_CLIENT_SECRET"),
		}, "clients/packaging/recipe.read", "clients/packaging/recipe.write")
		if err != nil {
			return err
		}
		api := vewrecipes.NewClient(transport)
		for _, resource := range state.RootModule().Resources {
			if resource.Primary == nil || resource.Primary.ID == "" {
				continue
			}
			switch resource.Type {
			case "vew_recipe_version":
				recipeID := resource.Primary.Attributes["recipe_id"]
				if recipeID == "" {
					return fmt.Errorf("disposable recipe version %q is missing recipe_id", resource.Primary.ID)
				}
				version, err := api.GetRecipeVersion(context.Background(), projectID, recipeID, resource.Primary.ID)
				if err != nil && !vew.IsNotFound(err) {
					return fmt.Errorf("read disposable recipe version %q: %w", resource.Primary.ID, err)
				}
				if err == nil && !strings.EqualFold(version.Status, "RETIRED") {
					return fmt.Errorf("disposable recipe version %q status = %q, want RETIRED", resource.Primary.ID, version.Status)
				}
			case "vew_recipe":
				recipe, err := api.GetRecipe(context.Background(), projectID, resource.Primary.ID)
				if err != nil && !vew.IsNotFound(err) {
					return fmt.Errorf("read disposable recipe %q: %w", resource.Primary.ID, err)
				}
				if err == nil && !strings.EqualFold(recipe.Status, "ARCHIVED") {
					return fmt.Errorf("disposable recipe %q status = %q, want ARCHIVED", resource.Primary.ID, recipe.Status)
				}
			}
		}
		return nil
	}
}
