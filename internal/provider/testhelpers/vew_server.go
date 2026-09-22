package testhelpers

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/elva-labs/terraform-provider-vew/internal/vew/components"
)

const (
	testClientID     = "terraform-test-client"
	testClientSecret = "terraform-test-secret"
	testAccessToken  = "terraform-test-access-token"
)

type VEWServer struct {
	t *testing.T

	mu             sync.Mutex
	server         *httptest.Server
	component      components.Component
	archived       bool
	notFound       bool
	createCalls    int
	getCalls       int
	updateCalls    int
	archiveCalls   int
	idempotencyKey string
	create         components.CreateComponentInput
	update         components.UpdateComponentInput
	failGets       int
	failGetDetail  string
}

func NewVEWServer(t *testing.T) *VEWServer {
	t.Helper()
	fake := &VEWServer{t: t}
	fake.server = httptest.NewServer(http.HandlerFunc(fake.handle))
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *VEWServer) handle(w http.ResponseWriter, request *http.Request) {
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

func (f *VEWServer) handleToken(w http.ResponseWriter, request *http.Request) {
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

func (f *VEWServer) handleCreate(w http.ResponseWriter, request *http.Request) {
	payload, ok := exactJSONBody(request, "componentName", "componentDescription", "componentPlatform", "componentSupportedArchitectures", "componentSupportedOsVersions")
	if !ok {
		f.writeProblem(w, http.StatusBadRequest, "invalid component create request")
		return
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		f.writeProblem(w, http.StatusBadRequest, "invalid component create request")
		return
	}
	var name, description, platform string
	var architectures, osVersions []string
	if json.Unmarshal(fields["componentName"], &name) != nil ||
		json.Unmarshal(fields["componentDescription"], &description) != nil ||
		json.Unmarshal(fields["componentPlatform"], &platform) != nil ||
		json.Unmarshal(fields["componentSupportedArchitectures"], &architectures) != nil ||
		json.Unmarshal(fields["componentSupportedOsVersions"], &osVersions) != nil {
		f.writeProblem(w, http.StatusBadRequest, "invalid component create request")
		return
	}
	input := components.CreateComponentInput{Name: name, Description: description, Platform: platform, SupportedArchitectures: architectures, SupportedOSVersions: osVersions}
	f.mu.Lock()
	f.createCalls++
	f.idempotencyKey = request.Header.Get("Idempotency-Key")
	f.create = input
	f.archived, f.notFound = false, false
	f.component = components.Component{
		ID: "cmp-123", Name: input.Name, Description: input.Description, Platform: input.Platform,
		SupportedArchitectures: input.SupportedArchitectures, SupportedOSVersions: input.SupportedOSVersions,
		Status: "CREATED", CreatedAt: "2026-09-21T12:00:00Z", CreatedBy: "terraform-test-user",
		UpdatedAt: "2026-09-21T12:00:00Z", UpdatedBy: "terraform-test-user",
	}
	f.mu.Unlock()
	f.writeJSON(w, http.StatusCreated, map[string]any{"componentId": "cmp-123"})
}

func (f *VEWServer) handleGet(w http.ResponseWriter) {
	f.mu.Lock()
	f.getCalls++
	component, missing := f.component, f.notFound
	failing := f.failGets > 0
	if failing {
		f.failGets--
	}
	failureDetail := f.failGetDetail
	f.mu.Unlock()
	if failing {
		f.writeProblem(w, http.StatusServiceUnavailable, failureDetail)
		return
	}
	if missing {
		f.writeProblem(w, http.StatusNotFound, "component not found")
		return
	}
	f.writeJSON(w, http.StatusOK, map[string]any{"component": fakeComponentJSON(component)})
}

func (f *VEWServer) handleUpdate(w http.ResponseWriter, request *http.Request) {
	payload, ok := exactJSONBody(request, "componentDescription")
	if !ok {
		f.writeProblem(w, http.StatusBadRequest, "invalid component update request")
		return
	}
	var input components.UpdateComponentInput
	if err := json.Unmarshal(payload, &input); err != nil {
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

func exactJSONBody(request *http.Request, expectedKeys ...string) ([]byte, bool) {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return nil, false
	}
	payload, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil || len(fields) != len(expectedKeys) {
		return nil, false
	}
	for _, key := range expectedKeys {
		if _, ok := fields[key]; !ok {
			return nil, false
		}
	}
	return payload, true
}

func (f *VEWServer) handleArchive(w http.ResponseWriter) {
	f.mu.Lock()
	f.archiveCalls++
	f.archived = true
	f.component.Status = "ARCHIVED"
	f.mu.Unlock()
	f.writeJSON(w, http.StatusNoContent, nil)
}

func (f *VEWServer) writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if value != nil {
		if err := json.NewEncoder(w).Encode(value); err != nil {
			f.t.Errorf("encode fake VEW response: %v", err)
		}
	}
}

func (f *VEWServer) writeProblem(w http.ResponseWriter, status int, detail string) {
	f.writeJSON(w, status, map[string]any{"status": status, "title": http.StatusText(status), "detail": detail, "code": fmt.Sprintf("HTTP_%d", status), "retryable": false})
}

func fakeComponentJSON(component components.Component) map[string]any {
	return map[string]any{
		"componentId":                     component.ID,
		"componentName":                   component.Name,
		"componentDescription":            component.Description,
		"componentPlatform":               component.Platform,
		"componentSupportedArchitectures": component.SupportedArchitectures,
		"componentSupportedOsVersions":    component.SupportedOSVersions,
		"status":                          component.Status,
		"createDate":                      component.CreatedAt,
		"createdBy":                       component.CreatedBy,
		"lastUpdateDate":                  component.UpdatedAt,
		"lastUpdatedBy":                   component.UpdatedBy,
	}
}

// ProviderConfig configures a provider to use this fake VEW server.
func (f *VEWServer) ProviderConfig() string {
	return fmt.Sprintf(`
provider "vew" {
  api_url       = %q
  token_url     = %q
  client_id     = %q
  client_secret = %q
}
`, f.server.URL, f.server.URL+"/oauth/token", testClientID, testClientSecret)
}
