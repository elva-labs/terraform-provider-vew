package components

import (
	"context"
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
