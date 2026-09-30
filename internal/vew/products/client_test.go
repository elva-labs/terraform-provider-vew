package products

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
)

const testKey = "9f135a8d-47f2-4a8a-bc2a-8cba77df05b6"

func productTransport(t *testing.T, serverURL, scope string) *vew.Transport {
	t.Helper()
	transport, err := vew.NewTransportWithScopes(vew.Config{
		APIURL: serverURL, TokenURL: serverURL + "/oauth/token", ClientID: "client", ClientSecret: "secret",
	}, scope)
	if err != nil {
		t.Fatal(err)
	}
	return transport
}

func productToken(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/oauth/token" {
		return false
	}
	_, _ = io.WriteString(w, `{"access_token":"`+r.FormValue("scope")+`","expires_in":3600}`)
	return true
}

func newTestClient(t *testing.T, serverURL string) *Client {
	t.Helper()
	return NewClient(
		productTransport(t, serverURL, "clients/publishing/product.write"),
		productTransport(t, serverURL, "clients/publishing/product.read"),
	)
}

func TestCreateProductSendsKeyScopeAndBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if productToken(w, r) {
			return
		}
		body, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodPost || r.URL.EscapedPath() != "/projects/project%2Fone/products" {
			t.Errorf("request = %s %s", r.Method, r.URL.EscapedPath())
		}
		if got := r.Header.Get("Authorization"); got != "Bearer clients/publishing/product.write" {
			t.Errorf("authorization = %q", got)
		}
		if got := r.Header.Get("Idempotency-Key"); got != testKey {
			t.Errorf("idempotency key = %q", got)
		}
		if want := `{"productName":"Workbench","productType":"WORKBENCH","productDescription":"","technologyId":"tech-1"}`; string(body) != want {
			t.Errorf("body = %s", body)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"productId":"prod-1"}`)
	}))
	defer server.Close()
	id, err := newTestClient(t, server.URL).CreateProduct(context.Background(), "project/one", CreateProductInput{Name: "Workbench", Type: "WORKBENCH", TechnologyID: "tech-1"}, testKey)
	if err != nil || id != "prod-1" {
		t.Fatalf("CreateProduct = %q, %v", id, err)
	}
}

func TestCreateProductRejectsInvalidInput(t *testing.T) {
	client := NewClient(nil, nil)
	if _, err := client.CreateProduct(context.Background(), "project", CreateProductInput{Name: "Workbench"}, "not-a-uuid"); err == nil {
		t.Fatal("expected invalid idempotency key error")
	}
	if _, err := client.CreateProduct(context.Background(), "project", CreateProductInput{Name: " "}, testKey); err == nil {
		t.Fatal("expected empty name error")
	}
	if _, err := client.CreateProduct(context.Background(), " ", CreateProductInput{Name: "Workbench"}, testKey); err == nil {
		t.Fatal("expected empty project error")
	}
}

func TestGetProductUsesReadScope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if productToken(w, r) {
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != "/projects/project/products/prod-1" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer clients/publishing/product.read" {
			t.Errorf("authorization = %q", got)
		}
		_, _ = io.WriteString(w, `{"projectId":"project","productId":"prod-1","productName":"Workbench","productType":"WORKBENCH","productDescription":"d","technologyId":"tech-1","technologyName":"Tech","status":"CREATED","availableStages":["DEV"],"createDate":"c","lastUpdateDate":"u"}`)
	}))
	defer server.Close()
	product, err := newTestClient(t, server.URL).GetProduct(context.Background(), "project", "prod-1")
	if err != nil || product.ID != "prod-1" || product.TechnologyName != "Tech" || len(product.AvailableStages) != 1 || product.Archived() {
		t.Fatalf("GetProduct = %#v, %v", product, err)
	}
}

func TestUpdateProductSendsMutableFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if productToken(w, r) {
			return
		}
		body, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodPut || string(body) != `{"productName":"New","productDescription":"d"}` {
			t.Errorf("request = %s %s", r.Method, body)
		}
		_, _ = io.WriteString(w, `{"productId":"prod-1"}`)
	}))
	defer server.Close()
	if err := newTestClient(t, server.URL).UpdateProduct(context.Background(), "project", "prod-1", UpdateProductInput{Name: "New", Description: "d"}); err != nil {
		t.Fatal(err)
	}
}

func TestArchiveProduct(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    int
		body      string
		wantDone  bool
		wantDelay time.Duration
	}{
		{"archiving", http.StatusAccepted, `{"productId":"prod-1"}`, false, 5 * time.Second},
		{"archived", http.StatusNoContent, "", true, 0},
		{"absent", http.StatusNotFound, `{"code":"NOT_FOUND"}`, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if productToken(w, r) {
					return
				}
				if r.Method != http.MethodDelete {
					t.Errorf("method = %s", r.Method)
				}
				if tc.status == http.StatusAccepted {
					w.Header().Set("Retry-After", "5")
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			done, delay, err := newTestClient(t, server.URL).ArchiveProduct(context.Background(), "project", "prod-1")
			if err != nil || done != tc.wantDone || delay != tc.wantDelay {
				t.Fatalf("ArchiveProduct = %v, %v, %v", done, delay, err)
			}
		})
	}
}

func TestProductErrorsAreSanitized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if productToken(w, r) {
			return
		}
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = io.WriteString(w, `{"code":"TECHNOLOGY_NOT_FOUND","detail":"secret detail\n","requestId":"req-1"}`)
	}))
	defer server.Close()
	_, err := newTestClient(t, server.URL).GetProduct(context.Background(), "project", "prod-1")
	var apiErr *vew.APIError
	if !errors.As(err, &apiErr) || apiErr.Problem.Code != "TECHNOLOGY_NOT_FOUND" || apiErr.Problem.RequestID != "req-1" || strings.Contains(err.Error(), "secret") {
		t.Fatalf("error = %v", err)
	}
}
