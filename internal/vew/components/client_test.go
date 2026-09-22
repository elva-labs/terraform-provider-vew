package components

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
)

func TestClientCreateComponentMapsRequestAndResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/oauth/token" {
			_, _ = io.WriteString(w, `{"access_token":"token","expires_in":3600}`)
			return
		}
		if request.URL.Path != "/projects/proj-1/components" || request.Header.Get("Idempotency-Key") == "" {
			t.Fatalf("request = %s %q", request.URL.Path, request.Header.Get("Idempotency-Key"))
		}
		_, _ = io.WriteString(w, `{"componentId":"cmp-123"}`)
	}))
	defer server.Close()
	transport, err := vew.NewTransport(vew.Config{APIURL: server.URL, TokenURL: server.URL + "/oauth/token", ClientID: "id", ClientSecret: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	id, err := NewClient(transport).CreateComponent(context.Background(), "proj-1", CreateComponentInput{Name: "name", Description: "description", Platform: "Linux", SupportedArchitectures: []string{"arm64"}, SupportedOSVersions: []string{"Ubuntu 24"}})
	if err != nil || id != "cmp-123" {
		t.Fatalf("id/err = %q/%v", id, err)
	}
}

func TestClientCreateComponentReusesStableKeyAndBodyAfterInProgressResponse(t *testing.T) {
	var bodies [][]byte
	var keys []string
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/oauth/token" {
			_, _ = io.WriteString(w, `{"access_token":"token","expires_in":3600}`)
			return
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		bodies, keys = append(bodies, body), append(keys, request.Header.Get("Idempotency-Key"))
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `{"code":"IDEMPOTENCY_REQUEST_IN_PROGRESS"}`)
			return
		}
		_, _ = io.WriteString(w, `{"componentId":"cmp-123"}`)
	}))
	defer server.Close()
	transport, err := vew.NewTransport(vew.Config{APIURL: server.URL, TokenURL: server.URL + "/oauth/token", ClientID: "id", ClientSecret: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(transport)
	client.idempotencyKey = func() string { return "stable-key" }
	if _, err := client.CreateComponent(context.Background(), "proj", CreateComponentInput{Name: "n"}); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 || keys[0] != "stable-key" || keys[0] != keys[1] || !bytes.Equal(bodies[0], bodies[1]) {
		t.Fatalf("calls/keys/bodies = %d/%q/%q", len(bodies), keys, bodies)
	}
}

func TestClientGetComponentDecodesEnvelope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/oauth/token" {
			_, _ = io.WriteString(w, `{"access_token":"token","expires_in":3600}`)
			return
		}
		if request.Method != http.MethodGet || request.URL.Path != "/projects/proj/components/cmp" {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		_, _ = io.WriteString(w, `{"component":{"componentId":"cmp","componentName":"name","componentDescription":"description","componentPlatform":"Linux","componentSupportedArchitectures":["arm64"],"componentSupportedOsVersions":["Ubuntu 24"],"status":"CREATED","createDate":"2026-09-21T00:00:00Z","createdBy":"creator","lastUpdateDate":"2026-09-22T00:00:00Z","lastUpdatedBy":"updater"}}`)
	}))
	defer server.Close()
	transport, err := vew.NewTransport(vew.Config{APIURL: server.URL, TokenURL: server.URL + "/oauth/token", ClientID: "id", ClientSecret: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := NewClient(transport).GetComponent(context.Background(), "proj", "cmp")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "cmp" || got.Status != "CREATED" || got.Name != "name" || got.UpdatedBy != "updater" || len(got.SupportedArchitectures) != 1 || got.SupportedArchitectures[0] != "arm64" {
		t.Fatalf("component = %#v", got)
	}
}

func TestClientUpdateComponentSendsDescriptionOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/oauth/token" {
			_, _ = io.WriteString(w, `{"access_token":"token","expires_in":3600}`)
			return
		}
		if request.Method != http.MethodPut {
			t.Fatalf("method = %s", request.Method)
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body) != 1 || body["componentDescription"] != "new description" {
			t.Fatalf("body = %#v", body)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	transport, err := vew.NewTransport(vew.Config{APIURL: server.URL, TokenURL: server.URL + "/oauth/token", ClientID: "id", ClientSecret: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if err := NewClient(transport).UpdateComponent(context.Background(), "proj", "cmp", UpdateComponentInput{Description: "new description"}); err != nil {
		t.Fatal(err)
	}
}

func TestClientArchiveComponentAcceptsNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/oauth/token" {
			_, _ = io.WriteString(w, `{"access_token":"token","expires_in":3600}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"status":404,"detail":"gone"}`)
	}))
	defer server.Close()
	transport, err := vew.NewTransport(vew.Config{APIURL: server.URL, TokenURL: server.URL + "/oauth/token", ClientID: "id", ClientSecret: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if err := NewClient(transport).ArchiveComponent(context.Background(), "proj", "cmp"); err != nil {
		t.Fatal(err)
	}
}

func TestClientRejectsEmptyResourceIdentifiers(t *testing.T) {
	transport, err := vew.NewTransport(vew.Config{APIURL: "https://api.example", TokenURL: "https://token.example", ClientID: "id", ClientSecret: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(transport)
	if _, err := client.GetComponent(context.Background(), "", "cmp"); err == nil {
		t.Fatal("accepted empty project ID")
	}
	if _, err := client.GetComponent(context.Background(), "project", ""); err == nil {
		t.Fatal("accepted empty component ID")
	}
}
