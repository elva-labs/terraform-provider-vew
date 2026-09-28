package pipelines_test

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

func TestPipelineDataSourcesProtocolReadOnly(t *testing.T) {
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
			writePipelineDataSourceJSON(w, http.StatusOK, map[string]any{"access_token": "test-token", "expires_in": 3600})
			return
		}
		if request.Method != http.MethodGet {
			t.Errorf("unexpected mutating or polling request: %s %s", request.Method, request.URL.Path)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		requests = append(requests, request.Method+" "+request.URL.Path)
		switch request.URL.Path {
		case "/projects/project/pipelines/pipeline-z":
			writePipelineDataSourceJSON(w, http.StatusOK, map[string]any{"pipeline": pipelineProtocolObject("pipeline-z", "RETIRED", nil)})
		case "/projects/project/pipelines":
			writePipelineDataSourceJSON(w, http.StatusOK, map[string]any{"pipelines": []any{
				pipelineProtocolObject("pipeline-z", "RETIRED", nil),
				pipelineProtocolObject("pipeline-a", "FAILED", "product-1"),
			}})
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
data "vew_pipeline" "existing" {
  project_id = "project"
  pipeline_id = "pipeline-z"
}
data "vew_pipelines" "all" {
  project_id = "project"
}
`, server.URL, server.URL+"/oauth/token")
	testresource.Test(t, testresource.TestCase{
		IsUnitTest: true,
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"vew": providerserver.NewProtocol6WithError(rootprovider.New("test")()),
		},
		CheckDestroy: func(*terraform.State) error {
			mu.Lock()
			defer mu.Unlock()
			exactPath := "GET /projects/project/pipelines/pipeline-z"
			listPath := "GET /projects/project/pipelines"
			got := make(map[string]int, len(requests))
			for _, request := range requests {
				got[request]++
			}
			if len(got) != 2 || got[exactPath] == 0 || got[exactPath] != got[listPath] {
				return fmt.Errorf("pipeline data-source requests = %v; expected equal nonzero exact/list GET counts", got)
			}
			if len(scopes) == 0 {
				return fmt.Errorf("no pipeline read token was requested")
			}
			for _, scope := range scopes {
				if scope != "clients/packaging/pipeline.read" {
					return fmt.Errorf("pipeline data-source scopes = %q", scopes)
				}
			}
			return nil
		},
		Steps: []testresource.TestStep{{Config: config, Check: testresource.ComposeTestCheckFunc(
			testresource.TestCheckResourceAttr("data.vew_pipeline.existing", "pipeline_id", "pipeline-z"),
			testresource.TestCheckResourceAttr("data.vew_pipeline.existing", "status", "RETIRED"),
			testresource.TestCheckNoResourceAttr("data.vew_pipeline.existing", "product_id"),
			testresource.TestCheckResourceAttr("data.vew_pipelines.all", "pipelines.#", "2"),
			testresource.TestCheckResourceAttr("data.vew_pipelines.all", "pipelines.0.pipeline_id", "pipeline-a"),
			testresource.TestCheckResourceAttr("data.vew_pipelines.all", "pipelines.0.status", "FAILED"),
			testresource.TestCheckResourceAttr("data.vew_pipelines.all", "pipelines.1.pipeline_id", "pipeline-z"),
		)}},
	})
}

func pipelineProtocolObject(id, status string, productID any) map[string]any {
	return map[string]any{
		"projectId": "project", "pipelineId": id, "pipelineName": id, "pipelineDescription": "desc",
		"recipeId": "recipe", "recipeName": "recipe", "recipeVersionId": "version", "recipeVersionName": "1.0.0",
		"buildInstanceTypes": []string{"m8i.large"}, "pipelineSchedule": "cron", "productId": productID,
		"status": status, "distributionConfigArn": nil, "infrastructureConfigArn": nil, "pipelineArn": nil,
		"createDate": "2026-01-01", "createdBy": "tester", "lastUpdateDate": "2026-01-02", "lastUpdatedBy": "tester",
	}
}

func writePipelineDataSourceJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
