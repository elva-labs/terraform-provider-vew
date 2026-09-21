package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
	"uuid"
)

func TestClientCreateComponentMapsRequestAndResponse(t *testing.T) {
	var gotPath, gotKey string
	var gotInput map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotKey = r.URL.Path, r.Header.Get("Idempotency-Key")
		if err := json.NewDecoder(r.Body).Decode(&gotInput); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"componentId":"cmp-123"}`)
	}))
	defer server.Close()
	c := testClient(t, server.URL, server.Client(), staticTokenSource{})
	c.idempotencyKey = func() string { return "key-1" }

	id, err := c.CreateComponent(context.Background(), "proj-1", CreateComponentInput{Name: "name", Description: "description", Platform: "Linux", SupportedArchitectures: []string{"arm64"}, SupportedOSVersions: []string{"Ubuntu 24"}})
	if err != nil {
		t.Fatal(err)
	}
	if id != "cmp-123" || gotPath != "/projects/proj-1/components" || gotKey != "key-1" {
		t.Fatalf("id/path/key = %q/%q/%q", id, gotPath, gotKey)
	}
	if len(gotInput) != 5 || gotInput["componentName"] != "name" || gotInput["componentDescription"] != "description" || gotInput["componentPlatform"] != "Linux" || !stringSlicesEqual(gotInput["componentSupportedArchitectures"], []string{"arm64"}) || !stringSlicesEqual(gotInput["componentSupportedOsVersions"], []string{"Ubuntu 24"}) {
		t.Fatalf("input = %#v", gotInput)
	}
}

func TestClientCreateComponentUsesNonEmptyUUIDIdempotencyKey(t *testing.T) {
	var keys []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		_, _ = io.WriteString(w, `{"componentId":"cmp-123"}`)
	}))
	defer server.Close()
	c, err := New(Config{APIURL: server.URL, TokenURL: "https://token.example", ClientID: "id", ClientSecret: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	c.httpClient, c.tokens = server.Client(), staticTokenSource{}

	for range 2 {
		if _, err := c.CreateComponent(context.Background(), "project", CreateComponentInput{Name: "name"}); err != nil {
			t.Fatal(err)
		}
	}
	if len(keys) != 2 {
		t.Fatalf("keys = %v", keys)
	}
	for _, key := range keys {
		if key == "" {
			t.Fatal("idempotency key is empty")
		}
		if _, err := uuid.Parse(key); err != nil {
			t.Fatalf("idempotency key %q is not a UUID: %v", key, err)
		}
	}
}

func TestClientCreateComponentReusesKeyAndBodyAfterLostResponse(t *testing.T) {
	var bodies [][]byte
	var keys []string
	c := testClient(t, "https://vew.example", &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		bodies, keys = append(bodies, body), append(keys, r.Header.Get("Idempotency-Key"))
		if len(bodies) == 1 {
			return nil, io.ErrUnexpectedEOF
		}
		return jsonResponse(r, http.StatusCreated, `{"componentId":"cmp-123"}`), nil
	})}, staticTokenSource{})
	c.idempotencyKey = func() string { return "stable-key" }
	c.sleep = func(context.Context, time.Duration) error { return nil }

	if _, err := c.CreateComponent(context.Background(), "proj", CreateComponentInput{Name: "n"}); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 || keys[0] == "" || keys[0] != keys[1] || !bytes.Equal(bodies[0], bodies[1]) {
		t.Fatalf("calls=%d keys=%q bodies=%q", len(bodies), keys, bodies)
	}
}

func TestClientCreateComponentHonorsRetryAfterForInProgressOperation(t *testing.T) {
	calls := 0
	var delays []time.Duration
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "3")
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `{"status":409,"code":"IDEMPOTENCY_REQUEST_IN_PROGRESS","retryable":true}`)
			return
		}
		_, _ = io.WriteString(w, `{"componentId":"cmp-123"}`)
	}))
	defer server.Close()
	c := testClient(t, server.URL, server.Client(), staticTokenSource{})
	c.sleep = func(_ context.Context, d time.Duration) error { delays = append(delays, d); return nil }
	if _, err := c.CreateComponent(context.Background(), "proj", CreateComponentInput{Name: "n"}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(delays) != 1 || delays[0] != 3*time.Second {
		t.Fatalf("calls/delays = %d/%v", calls, delays)
	}
}

func TestClientCreateComponentDoesNotRetryReusedIdempotencyKey(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Retry-After", "3")
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"status":409,"code":"IDEMPOTENCY_KEY_REUSED","retryable":true}`)
	}))
	defer server.Close()
	c := testClient(t, server.URL, server.Client(), staticTokenSource{})
	c.sleep = func(context.Context, time.Duration) error {
		t.Fatal("retried reused idempotency key")
		return nil
	}
	if _, err := c.CreateComponent(context.Background(), "proj", CreateComponentInput{Name: "n"}); err == nil {
		t.Fatal("CreateComponent accepted reused idempotency key")
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestClientHonorsHTTPDateRetryAfter(t *testing.T) {
	calls := 0
	var delay time.Duration
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", time.Now().UTC().Add(10*time.Second).Format(http.TimeFormat))
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = io.WriteString(w, `{"component":{"componentId":"cmp"}}`)
	}))
	defer server.Close()
	c := testClient(t, server.URL, server.Client(), staticTokenSource{})
	c.sleep = func(_ context.Context, got time.Duration) error { delay = got; return nil }
	if _, err := c.GetComponent(context.Background(), "proj", "cmp"); err != nil {
		t.Fatal(err)
	}
	if delay < 8*time.Second || delay > 10*time.Second {
		t.Fatalf("delay = %v, want HTTP-date delay", delay)
	}
}

func TestClientRefreshesTokenOnceAfterUnauthorized(t *testing.T) {
	tokens := &tokenSourceStub{values: []string{"old", "new"}}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") == "Bearer old" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Authorization") != "Bearer new" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		_, _ = io.WriteString(w, `{"component":{"componentId":"cmp-123"}}`)
	}))
	defer server.Close()
	c := testClient(t, server.URL, server.Client(), tokens)
	component, err := c.GetComponent(context.Background(), "proj", "cmp-123")
	if err != nil {
		t.Fatal(err)
	}
	if component.ID != "cmp-123" || calls != 2 || strings.Join(tokens.forces, ",") != "false,true" {
		t.Fatalf("component/calls/forces = %#v/%d/%v", component, calls, tokens.forces)
	}
}

func TestClientDoesNotRetryUnauthorizedResponseAfterForcedRefresh(t *testing.T) {
	tokens := &tokenSourceStub{values: []string{"old", "new"}}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"status":401,"retryable":true}`)
	}))
	defer server.Close()
	c := testClient(t, server.URL, server.Client(), tokens)
	c.sleep = func(context.Context, time.Duration) error { return nil }
	_, err := c.GetComponent(context.Background(), "proj", "cmp")
	if err == nil || calls != 2 || strings.Join(tokens.forces, ",") != "false,true" {
		t.Fatalf("error/calls/forces = %v/%d/%v", err, calls, tokens.forces)
	}
}

func TestClientGetComponentDecodesEnvelope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/projects/proj/components/cmp" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"component":{"componentId":"cmp","componentName":"name","componentDescription":"description","componentPlatform":"Linux","componentSupportedArchitectures":["arm64"],"componentSupportedOsVersions":["Ubuntu 24"],"status":"CREATED","createDate":"2026-09-21T00:00:00Z","createdBy":"creator","lastUpdateDate":"2026-09-22T00:00:00Z","lastUpdatedBy":"updater"}}`)
	}))
	defer server.Close()
	c := testClient(t, server.URL, server.Client(), staticTokenSource{})
	got, err := c.GetComponent(context.Background(), "proj", "cmp")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "cmp" || got.Status != "CREATED" || got.Name != "name" || got.Description != "description" || got.Platform != "Linux" || got.CreatedAt != "2026-09-21T00:00:00Z" || got.CreatedBy != "creator" || got.UpdatedAt != "2026-09-22T00:00:00Z" || got.UpdatedBy != "updater" || len(got.SupportedArchitectures) != 1 || got.SupportedArchitectures[0] != "arm64" || len(got.SupportedOSVersions) != 1 || got.SupportedOSVersions[0] != "Ubuntu 24" {
		t.Fatalf("component = %#v", got)
	}
}

func TestClientEscapesProjectAndComponentIdentifiersAsSinglePathSegments(t *testing.T) {
	var escapedPath string
	c := testClient(t, "https://vew.example/api", &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		escapedPath = r.URL.EscapedPath()
		return jsonResponse(r, http.StatusOK, `{"component":{"componentId":"cmp"}}`), nil
	})}, staticTokenSource{})
	if _, err := c.GetComponent(context.Background(), "project/../one", "component/../two"); err != nil {
		t.Fatal(err)
	}
	if escapedPath != "/api/projects/project%2F%2E%2E%2Fone/components/component%2F%2E%2E%2Ftwo" {
		t.Fatalf("escaped path = %q", escapedPath)
	}
}

func TestClientRejectsEmptyResourceIdentifiersBeforeSendingRequest(t *testing.T) {
	calls := 0
	c := testClient(t, "https://vew.example", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("request should not be sent")
	})}, staticTokenSource{})
	if _, err := c.CreateComponent(context.Background(), "", CreateComponentInput{Name: "name"}); err == nil {
		t.Fatal("CreateComponent accepted empty project ID")
	}
	if _, err := c.GetComponent(context.Background(), "", "cmp"); err == nil {
		t.Fatal("GetComponent accepted empty project ID")
	}
	if _, err := c.GetComponent(context.Background(), "project", ""); err == nil {
		t.Fatal("GetComponent accepted empty component ID")
	}
	if err := c.UpdateComponent(context.Background(), "", "cmp", UpdateComponentInput{}); err == nil {
		t.Fatal("UpdateComponent accepted empty project ID")
	}
	if err := c.UpdateComponent(context.Background(), "project", "", UpdateComponentInput{}); err == nil {
		t.Fatal("UpdateComponent accepted empty component ID")
	}
	if err := c.ArchiveComponent(context.Background(), "", "cmp"); err == nil {
		t.Fatal("ArchiveComponent accepted empty project ID")
	}
	if err := c.ArchiveComponent(context.Background(), "project", ""); err == nil {
		t.Fatal("ArchiveComponent accepted empty component ID")
	}
	if calls != 0 {
		t.Fatalf("sent %d requests for invalid identifiers", calls)
	}
}

func TestClientUpdateComponentSendsDescriptionOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Fatalf("method = %s", r.Method)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body) != 1 || body["componentDescription"] != "new description" {
			t.Fatalf("body = %#v", body)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	c := testClient(t, server.URL, server.Client(), staticTokenSource{})
	if err := c.UpdateComponent(context.Background(), "proj", "cmp", UpdateComponentInput{Description: "new description"}); err != nil {
		t.Fatal(err)
	}
}

func TestClientArchiveComponentAcceptsNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"status":404,"detail":"gone"}`)
	}))
	defer server.Close()
	c := testClient(t, server.URL, server.Client(), staticTokenSource{})
	if err := c.ArchiveComponent(context.Background(), "proj", "cmp"); err != nil {
		t.Fatal(err)
	}
}

func TestClientDecodesProblemResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"type":"https://example/problems/invalid","title":"Invalid","status":400,"detail":"name is required","code":"INVALID_NAME","requestId":"request-7","retryable":false}`)
	}))
	defer server.Close()
	c := testClient(t, server.URL, server.Client(), staticTokenSource{})
	_, err := c.GetComponent(context.Background(), "proj", "cmp")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusBadRequest || apiErr.Problem.Code != "INVALID_NAME" || !strings.Contains(err.Error(), "name is required") || !strings.Contains(err.Error(), "request-7") {
		t.Fatalf("error = %#v", err)
	}
}

func TestClientProblemErrorDoesNotExposeBearerToken(t *testing.T) {
	const token = "access-token-value"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"status":400,"detail":"authentication failed for access-token-value"}`)
	}))
	defer server.Close()
	c := testClient(t, server.URL, server.Client(), staticTokenSource{token: token})
	_, err := c.GetComponent(context.Background(), "proj", "cmp")
	if err == nil || strings.Contains(err.Error(), token) {
		t.Fatalf("error exposed bearer token: %v", err)
	}
}

func TestClientStopsRetrySleepWhenContextIsCancelled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	c := testClient(t, server.URL, server.Client(), staticTokenSource{})
	c.sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.GetComponent(ctx, "proj", "cmp")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context cancellation", err)
	}
}

func TestClientStopsAfterMaximumRetryAttempts(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(http.StatusServiceUnavailable) }))
	defer server.Close()
	c := testClient(t, server.URL, server.Client(), staticTokenSource{})
	c.maxAttempts = 3
	c.sleep = func(context.Context, time.Duration) error { return nil }
	_, err := c.GetComponent(context.Background(), "proj", "cmp")
	if err == nil || calls != 3 {
		t.Fatalf("error/calls = %v/%d", err, calls)
	}
}

func TestClientClosesEachRetryResponseBody(t *testing.T) {
	var bodies []*trackingReadCloser
	calls := 0
	c := testClient(t, "https://vew.example", &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		body := &trackingReadCloser{Reader: strings.NewReader(`{"component":{"componentId":"cmp"}}`)}
		bodies = append(bodies, body)
		if calls == 1 {
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: make(http.Header), Body: body, Request: r}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body, Request: r}, nil
	})}, staticTokenSource{})
	c.sleep = func(context.Context, time.Duration) error { return nil }
	if _, err := c.GetComponent(context.Background(), "proj", "cmp"); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 || !bodies[0].closed || !bodies[1].closed {
		t.Fatalf("response bodies were not all closed")
	}
}

func TestClientLimitsResponseBodyReads(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = io.WriteString(w, strings.Repeat("x", clientResponseBodyLimit+1))
	}))
	defer server.Close()
	c := testClient(t, server.URL, server.Client(), staticTokenSource{})
	c.sleep = func(context.Context, time.Duration) error { return nil }
	_, err := c.GetComponent(context.Background(), "proj", "cmp")
	if err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("oversized response made %d requests, want 1", calls)
	}
}

func TestClientRecoversFromUnauthorizedAfterTransientRetryBoundary(t *testing.T) {
	tokens := &tokenSourceStub{values: []string{"one", "two", "three", "four", "refreshed"}}
	calls := 0
	c := testClient(t, "https://vew.example", &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls <= 3 {
			return jsonResponse(r, http.StatusServiceUnavailable, ``), nil
		}
		if calls == 4 {
			return jsonResponse(r, http.StatusUnauthorized, ``), nil
		}
		return jsonResponse(r, http.StatusOK, `{"component":{"componentId":"cmp"}}`), nil
	})}, tokens)
	c.sleep = func(context.Context, time.Duration) error { return nil }
	component, err := c.GetComponent(context.Background(), "project", "cmp")
	if err != nil {
		t.Fatal(err)
	}
	if component.ID != "cmp" || calls != 5 || strings.Join(tokens.forces, ",") != "false,false,false,false,true" {
		t.Fatalf("component/calls/forces = %#v/%d/%v", component, calls, tokens.forces)
	}
}

func TestAPIErrorNotFound(t *testing.T) {
	err := fmtAPIError(http.StatusNotFound, Problem{Detail: "missing"})
	if !IsNotFound(err) || IsNotFound(errors.New("missing")) {
		t.Fatalf("IsNotFound = %v", IsNotFound(err))
	}
}

func testClient(t *testing.T, rawURL string, httpClient *http.Client, tokens TokenSource) *Client {
	t.Helper()
	baseURL, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	return &Client{baseURL: baseURL, httpClient: httpClient, tokens: tokens, maxAttempts: 4, sleep: sleepContext, idempotencyKey: func() string { return "test-key" }}
}

type tokenSourceStub struct {
	values []string
	forces []string
}

func (s *tokenSourceStub) Token(_ context.Context, force bool) (string, error) {
	s.forces = append(s.forces, strconv.FormatBool(force))
	if len(s.values) == 0 {
		return "token", nil
	}
	token := s.values[0]
	s.values = s.values[1:]
	return token, nil
}

type staticTokenSource struct{ token string }

func (s staticTokenSource) Token(context.Context, bool) (string, error) {
	if s.token != "" {
		return s.token, nil
	}
	return "token", nil
}

func stringSlicesEqual(value any, want []string) bool {
	got, ok := value.([]any)
	if !ok || len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func jsonResponse(r *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}
}

type trackingReadCloser struct {
	io.Reader
	closed bool
}

func (r *trackingReadCloser) Close() error { r.closed = true; return nil }
