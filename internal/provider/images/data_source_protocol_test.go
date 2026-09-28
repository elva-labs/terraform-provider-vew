package images_test

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

func TestImageDataSourcesProtocolReadOnly(t *testing.T) {
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
			writeImageJSON(w, http.StatusOK, map[string]any{"access_token": "test-token", "expires_in": 3600})
			return
		}
		if r.Method != http.MethodGet {
			t.Errorf("unexpected mutating, execute, or polling request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/projects/project/images/image-one":
			writeImageJSON(w, http.StatusOK, map[string]any{"image": map[string]any{
				"projectId": "project", "imageId": "image-one", "pipelineId": "pipeline-one", "status": "DELETED", "imageUpstreamId": nil,
			}})
		case "/projects/project/images":
			writeImageJSON(w, http.StatusOK, map[string]any{"images": []any{
				map[string]any{"projectId": "project", "imageId": "z-image", "pipelineId": "pipeline-one", "status": "CREATED", "imageUpstreamId": nil},
				map[string]any{"projectId": "project", "imageId": "a-image", "pipelineId": "pipeline-one", "status": "CREATED", "imageUpstreamId": "ami-a"},
				map[string]any{"projectId": "project", "imageId": "other-image", "pipelineId": "other-pipeline", "status": "FAILED", "imageUpstreamId": nil},
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
data "vew_image" "existing" {
  project_id = "project"
  image_id = "image-one"
}
data "vew_images" "pipeline" {
  project_id = "project"
  pipeline_id = "pipeline-one"
}
`, server.URL, server.URL+"/oauth/token")
	testresource.Test(t, testresource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){"vew": providerserver.NewProtocol6WithError(rootprovider.New("test")())},
		CheckDestroy: func(*terraform.State) error {
			mu.Lock()
			defer mu.Unlock()
			var exactReads, collectionReads int
			for _, request := range requests {
				switch request {
				case "GET /projects/project/images/image-one":
					exactReads++
				case "GET /projects/project/images":
					collectionReads++
				default:
					return fmt.Errorf("unexpected request %q", request)
				}
			}
			if exactReads == 0 || collectionReads == 0 || exactReads != collectionReads {
				return fmt.Errorf("expected equal nonzero exact/list GET counts, got exact=%d collection=%d (%v)", exactReads, collectionReads, requests)
			}
			if len(scopes) == 0 {
				return fmt.Errorf("no image read token was requested")
			}
			for _, scope := range scopes {
				if scope != "clients/packaging/pipeline.read" {
					return fmt.Errorf("unexpected image data-source scope %q", scope)
				}
			}
			return nil
		},
		Steps: []testresource.TestStep{{Config: config, Check: testresource.ComposeTestCheckFunc(
			testresource.TestCheckResourceAttr("data.vew_image.existing", "project_id", "project"),
			testresource.TestCheckResourceAttr("data.vew_image.existing", "image_id", "image-one"),
			testresource.TestCheckResourceAttr("data.vew_image.existing", "status", "DELETED"),
			testresource.TestCheckNoResourceAttr("data.vew_image.existing", "image_upstream_id"),
			testresource.TestCheckResourceAttr("data.vew_images.pipeline", "images.#", "2"),
			testresource.TestCheckResourceAttr("data.vew_images.pipeline", "images.0.image_id", "a-image"),
			testresource.TestCheckResourceAttr("data.vew_images.pipeline", "images.0.project_id", "project"),
			testresource.TestCheckResourceAttr("data.vew_images.pipeline", "images.0.image_upstream_id", "ami-a"),
			testresource.TestCheckResourceAttr("data.vew_images.pipeline", "images.1.image_id", "z-image"),
			testresource.TestCheckNoResourceAttr("data.vew_images.pipeline", "images.1.image_upstream_id"),
		)}},
	})
}

func writeImageJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		panic(err)
	}
}
