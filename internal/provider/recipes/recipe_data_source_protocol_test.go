package recipes_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	rootprovider "github.com/elva-labs/terraform-provider-vew/internal/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	testresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestRecipeDataSourcesProtocolReadOnly(t *testing.T) {
	var mu sync.Mutex
	var scopes []string
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/oauth/token" {
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			scopes = append(scopes, r.Form.Get("scope"))
			writeJSON(w, http.StatusOK, map[string]any{"access_token": "test-token", "expires_in": 3600})
			return
		}
		if r.Method != http.MethodGet {
			t.Errorf("unexpected mutating or polling request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/projects/project/recipes/recipe":
			writeJSON(w, http.StatusOK, map[string]any{"recipe": map[string]any{
				"recipeId": "recipe", "recipeName": "example", "recipeDescription": "desc", "recipePlatform": "Linux",
				"recipeArchitecture": "amd64", "recipeOsVersion": "Ubuntu 24", "status": "ARCHIVED",
			}})
		case "/projects/project/recipes/recipe/versions/version":
			writeJSON(w, http.StatusOK, map[string]any{"recipe_version": map[string]any{
				"recipeId": "recipe", "recipeVersionId": "version", "recipeVersionDescription": "historical",
				"recipeVersionVolumeSize": "30", "recipeVersionIntegrations": []string{},
				"effectiveComponentsVersions": []any{map[string]any{"componentId": "component", "componentName": "comp", "componentVersionId": "v1", "componentVersionName": "1", "componentVersionType": "MAIN", "order": 1}},
				"status":                      "FAILED",
			}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	providerConfig := fmt.Sprintf(`provider "vew" {
  api_url = %q
  token_url = %q
  client_id = "id"
  client_secret = "secret"
}
`, server.URL, server.URL+"/oauth/token")
	config := providerConfig + `data "vew_recipe" "existing" {
  project_id = "project"
  recipe_id = "recipe"
}
data "vew_recipe_version" "historical" {
  project_id = "project"
  recipe_id = "recipe"
  version_id = "version"
}
`
	testresource.Test(t, testresource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){"vew": providerserver.NewProtocol6WithError(rootprovider.New("test")())},
		CheckDestroy: func(*terraform.State) error {
			mu.Lock()
			defer mu.Unlock()
			requestCounts := map[string]int{}
			for _, request := range requests {
				switch request {
				case "GET /projects/project/recipes/recipe", "GET /projects/project/recipes/recipe/versions/version":
					requestCounts[request]++
				default:
					return fmt.Errorf("unexpected request %q", request)
				}
			}
			recipeReads := requestCounts["GET /projects/project/recipes/recipe"]
			versionReads := requestCounts["GET /projects/project/recipes/recipe/versions/version"]
			if recipeReads == 0 || recipeReads != versionReads {
				return fmt.Errorf("expected both recipe GET paths with equal nonzero counts, got recipe=%d version=%d (%v)", recipeReads, versionReads, requests)
			}
			for _, scope := range scopes {
				if scope != "clients/packaging/recipe.read" {
					return fmt.Errorf("unexpected recipe data-source scope %q", scope)
				}
			}
			if len(scopes) == 0 {
				return fmt.Errorf("no recipe read token was requested")
			}
			return nil
		},
		Steps: []testresource.TestStep{{Config: config, Check: testresource.ComposeTestCheckFunc(
			testresource.TestCheckResourceAttr("data.vew_recipe.existing", "recipe_id", "recipe"),
			testresource.TestCheckResourceAttr("data.vew_recipe.existing", "status", "ARCHIVED"),
			testresource.TestCheckResourceAttr("data.vew_recipe_version.historical", "version_id", "version"),
			testresource.TestCheckResourceAttr("data.vew_recipe_version.historical", "status", "FAILED"),
			testresource.TestCheckResourceAttr("data.vew_recipe_version.historical", "effective_components.0.component_id", "component"),
		)}},
	})
}
