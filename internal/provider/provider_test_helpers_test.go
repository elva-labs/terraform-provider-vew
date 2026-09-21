package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/elva-labs/terraform-provider-vew/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

const (
	testClientID     = "terraform-test-client"
	testClientSecret = "terraform-test-secret"
	testAccessToken  = "terraform-test-access-token"
)

type fakeVEWServer struct {
	t *testing.T

	mu             sync.Mutex
	server         *httptest.Server
	component      client.Component
	archived       bool
	notFound       bool
	createCalls    int
	getCalls       int
	updateCalls    int
	archiveCalls   int
	idempotencyKey string
	update         client.UpdateComponentInput
}

func newFakeVEWServer(t *testing.T) *fakeVEWServer {
	t.Helper()
	fake := &fakeVEWServer{t: t}
	fake.server = httptest.NewServer(http.HandlerFunc(fake.handle))
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *fakeVEWServer) handle(w http.ResponseWriter, request *http.Request) {
	if request.URL.Path == "/oauth/token" {
		f.handleToken(w, request)
		return
	}
	if request.Header.Get("Authorization") != "Bearer "+testAccessToken {
		f.writeProblem(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	switch request.URL.Path {
	case "/projects/prog-73488/components":
		if request.Method == http.MethodPost {
			f.handleCreate(w, request)
			return
		}
	case "/projects/prog-73488/components/cmp-123":
		switch request.Method {
		case http.MethodGet:
			f.handleGet(w)
			return
		case http.MethodPut:
			f.handleUpdate(w, request)
			return
		case http.MethodDelete:
			f.handleArchive(w)
			return
		}
	}
	f.writeProblem(w, http.StatusNotFound, "not found")
}

func (f *fakeVEWServer) handleToken(w http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		f.writeProblem(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	clientID, secret, ok := request.BasicAuth()
	if !ok || clientID != testClientID || secret != testClientSecret {
		f.writeProblem(w, http.StatusUnauthorized, "invalid client credentials")
		return
	}
	if err := request.ParseForm(); err != nil || request.Form.Get("grant_type") != "client_credentials" || request.Form.Get("scope") != "clients/packaging/component.read clients/packaging/component.write" {
		f.writeProblem(w, http.StatusBadRequest, "invalid token request")
		return
	}
	f.writeJSON(w, http.StatusOK, map[string]any{"access_token": testAccessToken, "expires_in": 3600})
}

func (f *fakeVEWServer) handleCreate(w http.ResponseWriter, request *http.Request) {
	var input client.CreateComponentInput
	if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
		f.writeProblem(w, http.StatusBadRequest, "invalid component create request")
		return
	}
	f.mu.Lock()
	f.createCalls++
	f.idempotencyKey = request.Header.Get("Idempotency-Key")
	f.archived, f.notFound = false, false
	f.component = client.Component{
		ID: "cmp-123", Name: input.Name, Description: input.Description, Platform: input.Platform,
		SupportedArchitectures: input.SupportedArchitectures, SupportedOSVersions: input.SupportedOSVersions,
		Status: "ACTIVE", CreatedAt: "2026-09-21T12:00:00Z", CreatedBy: "terraform-test-user",
		UpdatedAt: "2026-09-21T12:00:00Z", UpdatedBy: "terraform-test-user",
	}
	f.mu.Unlock()
	f.writeJSON(w, http.StatusCreated, map[string]string{"id": "cmp-123"})
}

func (f *fakeVEWServer) handleGet(w http.ResponseWriter) {
	f.mu.Lock()
	f.getCalls++
	component, missing := f.component, f.notFound
	f.mu.Unlock()
	if missing {
		f.writeProblem(w, http.StatusNotFound, "component not found")
		return
	}
	f.writeJSON(w, http.StatusOK, map[string]client.Component{"data": component})
}

func (f *fakeVEWServer) handleUpdate(w http.ResponseWriter, request *http.Request) {
	var input client.UpdateComponentInput
	if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
		f.writeProblem(w, http.StatusBadRequest, "invalid component update request")
		return
	}
	f.mu.Lock()
	f.updateCalls++
	f.update = input
	f.component.Description = input.Description
	f.component.UpdatedAt = "2026-09-21T12:01:00Z"
	f.component.UpdatedBy = "terraform-test-updater"
	f.mu.Unlock()
	f.writeJSON(w, http.StatusOK, map[string]string{"result": "updated"})
}

func (f *fakeVEWServer) handleArchive(w http.ResponseWriter) {
	f.mu.Lock()
	f.archiveCalls++
	f.archived = true
	f.component.Status = "ARCHIVED"
	f.mu.Unlock()
	f.writeJSON(w, http.StatusNoContent, nil)
}

func (f *fakeVEWServer) writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if value != nil {
		if err := json.NewEncoder(w).Encode(value); err != nil {
			f.t.Errorf("encode fake VEW response: %v", err)
		}
	}
}

func (f *fakeVEWServer) writeProblem(w http.ResponseWriter, status int, detail string) {
	f.writeJSON(w, status, client.Problem{Status: status, Title: http.StatusText(status), Detail: detail, Code: fmt.Sprintf("HTTP_%d", status)})
}

func testProviderConfig(fake *fakeVEWServer) string {
	return fmt.Sprintf(`
provider "vew" {
  api_url       = %q
  token_url     = %q
  client_id     = %q
  client_secret = %q
}
`, fake.server.URL, fake.server.URL+"/oauth/token", testClientID, testClientSecret)
}

func testProtoV6ProviderFactories() map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){
		"vew": providerserver.NewProtocol6WithError(New("test")()),
	}
}
