package images

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

func imageTestTransport(t *testing.T, url, scope string) *vew.Transport {
	t.Helper()
	transport, err := vew.NewTransportWithScopes(vew.Config{
		APIURL: url, TokenURL: url + "/oauth/token", ClientID: "id", ClientSecret: "secret",
	}, scope)
	if err != nil {
		t.Fatal(err)
	}
	return transport
}

func imageToken(w http.ResponseWriter, request *http.Request) bool {
	if request.URL.Path != "/oauth/token" {
		return false
	}
	if request.FormValue("scope") != "clients/packaging/pipeline.read" {
		panic("unexpected OAuth scope " + request.FormValue("scope"))
	}
	_, _ = io.WriteString(w, `{"access_token":"`+request.FormValue("scope")+`","expires_in":3600}`)
	return true
}

func TestGetImageRejectsMissingInputWithoutRequest(t *testing.T) {
	client := NewClient(nil)
	if _, err := client.GetImage(context.Background(), "", "image"); err == nil {
		t.Fatal("accepted empty project ID")
	}
	if _, err := client.GetImage(context.Background(), "project", ""); err == nil {
		t.Fatal("accepted empty image ID")
	}
}

func TestGetImageUsesReadScopeAndDecodesStatuses(t *testing.T) {
	for _, tc := range []struct {
		name, status, upstream string
	}{
		{"creating", "CREATING", ""},
		{"created-with-upstream", "CREATED", "ami-123"},
		{"created-without-upstream", "CREATED", ""},
		{"failed", "FAILED", ""},
		{"retired", "RETIRED", ""},
		{"deleted", "DELETED", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstreamJSON := "null"
			if tc.upstream != "" {
				upstreamJSON = `"` + tc.upstream + `"`
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if imageToken(w, request) {
					return
				}
				if request.Method != http.MethodGet || request.URL.EscapedPath() != "/projects/project%2Fone/images/image%2Fone" {
					t.Errorf("request = %s %s", request.Method, request.URL.EscapedPath())
				}
				if request.Header.Get("Authorization") != "Bearer clients/packaging/pipeline.read" || request.Header.Get("Idempotency-Key") != "" {
					t.Errorf("headers = %#v", request.Header)
				}
				w.Header().Set("Retry-After", "3")
				_, _ = io.WriteString(w, `{"image":{"projectId":"project/one","imageId":"image/one","pipelineId":"pipe-one","status":"`+tc.status+`","imageUpstreamId":`+upstreamJSON+`}}`)
			}))
			defer server.Close()
			client := NewClient(imageTestTransport(t, server.URL, "clients/packaging/pipeline.read"))
			image, err := client.GetImage(context.Background(), "project/one", "image/one")
			if err != nil || image.ProjectID != "project/one" || image.ID != "image/one" || image.PipelineID != "pipe-one" || image.Status != tc.status || image.RetryAfter != 3*time.Second {
				t.Fatalf("image = %#v, %v", image, err)
			}
			if tc.upstream == "" && image.UpstreamID != nil || tc.upstream != "" && (image.UpstreamID == nil || *image.UpstreamID != tc.upstream) {
				t.Fatalf("upstream = %#v", image.UpstreamID)
			}
		})
	}
}

func TestGetImagePreservesAPIErrorAndRejectsMalformedEnvelope(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"not-found", http.StatusNotFound, `{"title":"Not found","requestId":"req-123"}`},
		{"forbidden", http.StatusForbidden, `{"title":"Forbidden"}`},
		{"malformed", http.StatusOK, `{"image":{}}`},
		{"invalid-json", http.StatusOK, `{"image":`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if imageToken(w, request) {
					return
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			client := NewClient(imageTestTransport(t, server.URL, "clients/packaging/pipeline.read"))
			_, err := client.GetImage(context.Background(), "project", "image")
			if err == nil {
				t.Fatal("expected error")
			}
			if tc.status != http.StatusOK {
				var apiErr *vew.APIError
				if !errors.As(err, &apiErr) || apiErr.Status != tc.status {
					t.Fatalf("API error = %v", err)
				}
				if vew.IsNotFound(err) != (tc.status == http.StatusNotFound) {
					t.Fatalf("IsNotFound(%v) = %v", err, vew.IsNotFound(err))
				}
			} else if !strings.Contains(err.Error(), "missing image") {
				t.Fatalf("malformed error = %v", err)
			}
		})
	}
}

func TestListImagesReadScopeAndDecode(t *testing.T) {
	for _, body := range []string{
		`{"images":[]}`,
		`{"images":[{"imageId":"z-image","pipelineId":"pipeline","status":"RETIRED","imageUpstreamId":null},{"imageId":"a-image","pipelineId":"pipeline","status":"FAILED","imageUpstreamId":"ami-a"}]}`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			if imageToken(w, request) {
				return
			}
			if request.Method != http.MethodGet || request.URL.Path != "/projects/project/images" {
				t.Errorf("request = %s %s", request.Method, request.URL.Path)
			}
			if request.Header.Get("Authorization") != "Bearer clients/packaging/pipeline.read" {
				t.Errorf("authorization = %q", request.Header.Get("Authorization"))
			}
			_, _ = io.WriteString(w, body)
		}))
		client := NewClient(imageTestTransport(t, server.URL, "clients/packaging/pipeline.read"))
		images, err := client.ListImages(context.Background(), "project")
		server.Close()
		if err != nil {
			t.Fatalf("ListImages: %v", err)
		}
		if strings.Contains(body, "z-image") && (len(images) != 2 || images[0].ID != "z-image" || images[0].UpstreamID != nil || images[1].UpstreamID == nil || *images[1].UpstreamID != "ami-a") {
			t.Fatalf("images = %#v", images)
		}
		if strings.Contains(body, "[]") && len(images) != 0 {
			t.Fatalf("empty images = %#v", images)
		}
	}
}

func TestListImagesRejectsMalformedEnvelopeAndImage(t *testing.T) {
	for _, body := range []string{`{}`, `{"images":null}`, `{"images":`, `{"images":[{}]}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			if imageToken(w, request) {
				return
			}
			_, _ = io.WriteString(w, body)
		}))
		client := NewClient(imageTestTransport(t, server.URL, "clients/packaging/pipeline.read"))
		_, err := client.ListImages(context.Background(), "project")
		server.Close()
		if err == nil {
			t.Fatalf("accepted malformed list response %q", body)
		}
	}
	if _, err := NewClient(nil).ListImages(context.Background(), " "); err == nil {
		t.Fatal("accepted empty project ID")
	}
}

func TestListImagesPreservesAPIErrorMetadata(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    int
		body      string
		code      string
		requestID string
	}{
		{"forbidden", http.StatusForbidden, `{"status":403,"code":"ACCESS_DENIED","requestId":"req-forbidden"}`, "ACCESS_DENIED", "req-forbidden"},
		{"not found", http.StatusNotFound, `{"status":404,"code":"PROJECT_NOT_FOUND","requestId":"req-missing"}`, "PROJECT_NOT_FOUND", "req-missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if imageToken(w, request) {
					return
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			client := NewClient(imageTestTransport(t, server.URL, "clients/packaging/pipeline.read"))
			_, err := client.ListImages(context.Background(), "project")
			var apiErr *vew.APIError
			if !errors.As(err, &apiErr) || apiErr.Status != tc.status || apiErr.Problem.Code != tc.code || apiErr.Problem.RequestID != tc.requestID {
				t.Fatalf("ListImages error = %#v; want status=%d code=%q requestID=%q", err, tc.status, tc.code, tc.requestID)
			}
			if vew.IsNotFound(err) != (tc.status == http.StatusNotFound) {
				t.Fatalf("IsNotFound(%v) = %t", err, vew.IsNotFound(err))
			}
		})
	}
}
