package vew

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestTransportEscapesEachPathSegmentIndependently would fail if Do joined
// path segments before escaping them: slashes and dot segments would then be
// interpreted as routing syntax rather than identifier data.
func TestTransportEscapesEachPathSegmentIndependently(t *testing.T) {
	var escapedPath string
	transport, err := NewTransport(Config{APIURL: "https://vew.example", TokenURL: "https://token.example", ClientID: "id", ClientSecret: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	transport.httpClient = &http.Client{Transport: transportRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		escapedPath = request.URL.EscapedPath()
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
	})}
	transport.tokens = staticTokenSource{}

	_, _, err = transportDo(context.Background(), transport, http.MethodGet, []string{"projects", "project/../one", "components", "component/../two"}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if escapedPath != "/projects/project%2F%2E%2E%2Fone/components/component%2F%2E%2E%2Ftwo" {
		t.Fatalf("escaped path = %q", escapedPath)
	}
}

type transportRoundTripFunc func(*http.Request) (*http.Response, error)

func (f transportRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type staticTokenSource struct{}

func (staticTokenSource) Token(context.Context, bool) (string, error) { return "token", nil }

// transportDo keeps tests explicit about the public Transport boundary.
func transportDo(ctx context.Context, transport *Transport, method string, pathSegments []string, body []byte, idempotencyKey string) ([]byte, http.Header, error) {
	return transport.Do(ctx, method, pathSegments, body, idempotencyKey)
}
