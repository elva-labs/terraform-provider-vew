package components_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	testresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestComponentDataSourcesProtocolReadOnly(t *testing.T) {
	var mu sync.Mutex
	var scopes []string
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if request.URL.Path == "/oauth/token" {
			if err := request.ParseForm(); err != nil {
				t.Error(err)
			}
			scopes = append(scopes, request.Form.Get("scope"))
			writeComponentDataSourceJSON(w, http.StatusOK, map[string]any{"access_token": "test-token", "expires_in": 3600})
			return
		}
		if request.Method != http.MethodGet {
			t.Errorf("unexpected mutating or polling request: %s %s", request.Method, request.URL.Path)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		requests = append(requests, request.Method+" "+request.URL.Path)
		switch request.URL.Path {
		case "/projects/project/components/component":
			writeComponentDataSourceJSON(w, http.StatusOK, map[string]any{"component": map[string]any{
				"componentId": "component", "componentName": "example", "componentDescription": "desc",
				"componentPlatform": "Linux", "supportedArchitectures": []string{"arm64", "x86_64"},
				"supportedOSVersions": []string{"Ubuntu 24"}, "status": "ARCHIVED",
			}})
		case "/projects/project/components/component/versions/version":
			writeComponentDataSourceJSON(w, http.StatusOK, map[string]any{
				"component_version": map[string]any{
					"componentId": "component", "componentVersionId": "version", "componentVersionName": "1.0.0",
					"componentVersionDescription": "historical", "componentVersionDependencies": []any{},
					"softwareVendor": "vendor", "softwareVersion": "1.0.0", "licenseDashboard": nil,
					"notes": nil, "status": "RETIRED",
				},
				"componentVersionDefinition": map[string]any{"schemaVersion": 1, "commands": []any{}},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	config := fmt.Sprintf(`provider "vew" {
  api_url = %q
  token_url = %q
  client_id = "id"
  client_secret = "secret"
}
data "vew_component" "existing" {
  project_id = "project"
  component_id = "component"
}
data "vew_component_version" "historical" {
  project_id = "project"
  component_id = "component"
  version_id = "version"
}
`, server.URL, server.URL+"/oauth/token")
	testresource.Test(t, testresource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testProtoV6ProviderFactories(),
		CheckDestroy: func(*terraform.State) error {
			mu.Lock()
			defer mu.Unlock()
			gotRequests := make(map[string]int, len(requests))
			for _, request := range requests {
				gotRequests[request]++
			}
			componentPath := "GET /projects/project/components/component"
			versionPath := "GET /projects/project/components/component/versions/version"
			componentCount, componentFound := gotRequests[componentPath]
			versionCount, versionFound := gotRequests[versionPath]
			if !componentFound || !versionFound || componentCount == 0 || componentCount != versionCount || len(gotRequests) != 2 {
				return fmt.Errorf("component data-source requests = %v, want only %q and %q with equal nonzero counts", gotRequests, componentPath, versionPath)
			}
			if len(scopes) == 0 {
				return fmt.Errorf("component data-source did not request an OAuth scope")
			}
			for _, scope := range scopes {
				if scope != "clients/packaging/component.read" {
					return fmt.Errorf("component data-source scopes = %q", scopes)
				}
			}
			return nil
		},
		Steps: []testresource.TestStep{{Config: config, Check: testresource.ComposeTestCheckFunc(
			testresource.TestCheckResourceAttr("data.vew_component.existing", "component_id", "component"),
			testresource.TestCheckResourceAttr("data.vew_component.existing", "status", "ARCHIVED"),
			testresource.TestCheckResourceAttr("data.vew_component_version.historical", "version_id", "version"),
			testresource.TestCheckResourceAttr("data.vew_component_version.historical", "status", "RETIRED"),
			testresource.TestCheckResourceAttr("data.vew_component_version.historical", "definition_json", `{"commands":[],"schemaVersion":1}`),
		)}},
	})
}

func writeComponentDataSourceJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
