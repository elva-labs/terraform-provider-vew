package imageactions_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"context"
	rootprovider "github.com/elva-labs/terraform-provider-vew/internal/provider"
	"github.com/hashicorp/go-version"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	testresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"
)

const imageProtocolKey = "90827b61-8399-4d8a-9843-d75d1556fed0"

type imageTerraform1163Only struct{}

func (imageTerraform1163Only) CheckTerraformVersion(_ context.Context, request tfversion.CheckTerraformVersionRequest, response *tfversion.CheckTerraformVersionResponse) {
	want := version.Must(version.NewVersion("1.16.3"))
	if !request.TerraformVersion.Equal(want) {
		response.Skip = fmt.Sprintf("image-action protocol coverage is pinned to Terraform 1.16.3, found %s", request.TerraformVersion)
	}
}

type imageProtocolServer struct {
	server  *httptest.Server
	mu      sync.Mutex
	builds  int
	reads   int
	deletes int
	keys    []string
	bodies  []string
	status  string
}

func newImageProtocolServer(t *testing.T) *imageProtocolServer {
	t.Helper()
	f := &imageProtocolServer{status: "CREATED"}
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.server.Close)
	return f
}

func (f *imageProtocolServer) providerConfig() string {
	return fmt.Sprintf(`
provider "vew" {
  api_url       = %q
  token_url     = %q
  client_id     = "test-client"
  client_secret = "test-secret"
}
`, f.server.URL, f.server.URL+"/oauth/token")
}

func (f *imageProtocolServer) snapshot() (int, int, int, []string, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.builds, f.reads, f.deletes, append([]string(nil), f.keys...), append([]string(nil), f.bodies...)
}

func (f *imageProtocolServer) handle(w http.ResponseWriter, request *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	write := func(status int, value any) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(value)
	}
	if request.URL.Path == "/oauth/token" {
		write(http.StatusOK, map[string]any{"access_token": "token", "expires_in": 3600})
		return
	}
	if request.Header.Get("Authorization") != "Bearer token" {
		write(http.StatusUnauthorized, map[string]string{"code": "UNAUTHORIZED"})
		return
	}
	switch {
	case request.Method == http.MethodPost && request.URL.Path == "/projects/project/images":
		var body struct {
			PipelineID string `json:"pipelineId"`
		}
		decoder := json.NewDecoder(request.Body)
		if err := decoder.Decode(&body); err != nil || body.PipelineID != "pipeline" {
			write(http.StatusBadRequest, map[string]string{"code": "INVALID_REQUEST"})
			return
		}
		f.mu.Lock()
		f.builds++
		f.keys = append(f.keys, request.Header.Get("Idempotency-Key"))
		f.bodies = append(f.bodies, body.PipelineID)
		f.mu.Unlock()
		w.Header().Set("Retry-After", "0")
		write(http.StatusAccepted, map[string]string{"imageId": "image"})
	case request.Method == http.MethodGet && request.URL.Path == "/projects/project/images/image":
		f.mu.Lock()
		f.reads++
		status := f.status
		f.mu.Unlock()
		write(http.StatusOK, map[string]any{"image": map[string]any{
			"imageId": "image", "pipelineId": "pipeline", "status": status, "imageUpstreamId": "ami-1234567890abcdef0",
		}})
	case request.Method == http.MethodDelete && strings.HasPrefix(request.URL.Path, "/projects/project/images"):
		f.mu.Lock()
		f.deletes++
		f.mu.Unlock()
		write(http.StatusNoContent, nil)
	default:
		write(http.StatusNotFound, map[string]string{"code": "NOT_FOUND"})
	}
}

func imageProtocolFactories() map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){
		"vew": providerserver.NewProtocol6WithError(rootprovider.New("test")()),
	}
}

func TestImageBuildDeclarationAndRemovalDoNotInvokeProtocol(t *testing.T) {
	fake := newImageProtocolServer(t)
	base := fake.providerConfig() + `
resource "terraform_data" "unrelated" {
  input = "ordinary apply"
}
`
	declared := base + `
action "vew_image_build" "declared" {
  config {
    project_id      = "project"
    pipeline_id     = "pipeline"
    idempotency_key = "` + imageProtocolKey + `"
  }
}
`
	testresource.UnitTest(t, testresource.TestCase{
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{imageTerraform1163Only{}},
		ProtoV6ProviderFactories: imageProtocolFactories(),
		Steps: []testresource.TestStep{
			{Config: declared, Check: func(*terraform.State) error { return assertNoImageMutation(fake) }},
			{Config: base, Check: func(*terraform.State) error { return assertNoImageMutation(fake) }},
		},
	})
}

func assertNoImageMutation(fake *imageProtocolServer) error {
	builds, reads, deletes, _, _ := fake.snapshot()
	if builds != 0 || reads != 0 || deletes != 0 {
		return fmt.Errorf("declaration/removal made image requests: POST=%d GET=%d DELETE=%d", builds, reads, deletes)
	}
	return nil
}

func TestImageBuildDirectInvocationProtocol(t *testing.T) {
	fake := newImageProtocolServer(t)
	workspace := newImageTerraformCLIWorkspace(t, fake.providerConfig()+`
action "vew_image_build" "manual" {
  config {
    project_id      = "project"
    pipeline_id     = "pipeline"
    idempotency_key = "`+imageProtocolKey+`"
  }
}

resource "vew_pipeline" "unrelated" {
  project_id           = "project"
  name                 = "must-not-apply"
  description          = "direct action invocation excludes resources"
  recipe_id            = "recipe"
  recipe_version_id    = "recipe-version"
  build_instance_types = ["m8i.2xlarge"]
  schedule             = "0 0 * * ? *"
}
`)
	output := workspace.run("apply", "-invoke=action.vew_image_build.manual", "-auto-approve", "-input=false", "-no-color")
	if match := reservedImagePattern.FindStringSubmatch(output); len(match) != 2 || match[1] != "image" {
		t.Fatalf("direct invocation did not report reserved image ID in progress: %q", output)
	}
	builds, reads, deletes, keys, bodies := fake.snapshot()
	if builds != 1 || reads == 0 || deletes != 0 || len(keys) != 1 || keys[0] != imageProtocolKey || len(bodies) != 1 || bodies[0] != "pipeline" {
		t.Fatalf("direct invocation requests: POST=%d GET=%d DELETE=%d keys=%v bodies=%v", builds, reads, deletes, keys, bodies)
	}
}
