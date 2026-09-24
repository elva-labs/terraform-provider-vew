package recipes_test

import (
	"encoding/json"
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

func TestRecipeResourceProtocolLifecycle(t *testing.T) {
	var mu sync.Mutex
	created, archived, createCalls, getCalls, archiveCalls := false, false, 0, 0, 0
	var scopes []string
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
		if r.Header.Get("Authorization") != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/projects/project/recipes":
			createCalls++
			created = true
			if r.Header.Get("Idempotency-Key") == "" {
				t.Error("recipe create missing idempotency key")
			}
			var input map[string]any
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input["recipeName"] != "example" || input["recipeDescription"] != "description" || len(input) != 5 {
				t.Errorf("create input = %v, err = %v", input, err)
			}
			writeJSON(w, http.StatusCreated, map[string]string{"recipeId": "recipe"})
		case r.Method == http.MethodGet && r.URL.Path == "/projects/project/recipes/recipe":
			getCalls++
			if !created || archived {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"recipe": map[string]any{
				"recipeId": "recipe", "recipeName": "example", "recipeDescription": "description",
				"recipePlatform": "Linux", "recipeArchitecture": "amd64", "recipeOsVersion": "Ubuntu 24",
				"status": "CREATED", "createDate": "2026-09-23T00:00:00Z", "createdBy": "fixture",
				"lastUpdateDate": "2026-09-23T00:00:00Z", "lastUpdatedBy": "fixture",
			}})
		case r.Method == http.MethodDelete && r.URL.Path == "/projects/project/recipes/recipe":
			archiveCalls++
			archived = true
			w.WriteHeader(http.StatusNoContent)
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
	config := providerConfig + `resource "vew_recipe" "test" {
  project_id = "project"
  name = "example"
  description = "description"
  platform = "Linux"
  architecture = "amd64"
  os_version = "Ubuntu 24"
}
`
	testresource.Test(t, testresource.TestCase{
		IsUnitTest: true,
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"vew": providerserver.NewProtocol6WithError(rootprovider.New("test")()),
		},
		CheckDestroy: func(*terraform.State) error {
			mu.Lock()
			defer mu.Unlock()
			if !archived || archiveCalls != 1 || createCalls != 1 || getCalls == 0 {
				return fmt.Errorf("lifecycle calls: create=%d get=%d archive=%d archived=%t", createCalls, getCalls, archiveCalls, archived)
			}
			for _, scope := range scopes {
				if scope != "clients/packaging/recipe.read clients/packaging/recipe.write" {
					return fmt.Errorf("unexpected token scope %q", scope)
				}
			}
			return nil
		},
		Steps: []testresource.TestStep{{Config: config, Check: testresource.ComposeTestCheckFunc(
			testresource.TestCheckResourceAttr("vew_recipe.test", "id", "recipe"),
			testresource.TestCheckResourceAttr("vew_recipe.test", "status", "CREATED"),
			testresource.TestCheckResourceAttr("vew_recipe.test", "created_by", "fixture"),
		)}},
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
