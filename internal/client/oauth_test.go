package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testScope = "clients/packaging/component.read clients/packaging/component.write"

func TestOAuthTokenSourceRequestsClientCredentialsAndScopes(t *testing.T) {
	var gotMethod, gotUser, gotPassword string
	var gotForm url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotUser, gotPassword, _ = r.BasicAuth()
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		gotForm = r.PostForm
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "token-1", "expires_in": 120})
	}))
	defer server.Close()

	source, err := NewOAuthTokenSource(server.URL, "client-id", "client-secret", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	got, err := source.Token(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if got != "token-1" {
		t.Fatalf("token = %q, want token-1", got)
	}
	if gotMethod != http.MethodPost || gotUser != "client-id" || gotPassword != "client-secret" {
		t.Fatalf("request = %s basic auth %q/%q", gotMethod, gotUser, gotPassword)
	}
	if gotForm.Get("grant_type") != "client_credentials" || gotForm.Get("scope") != testScope {
		t.Fatalf("form = %v", gotForm)
	}
}

func TestOAuthTokenSourceCachesUnexpiredToken(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "cached", "expires_in": 120})
	}))
	defer server.Close()
	source, err := NewOAuthTokenSource(server.URL, "id", "secret", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if got, err := source.Token(context.Background(), false); err != nil || got != "cached" {
			t.Fatalf("Token() = %q, %v", got, err)
		}
	}
	if calls != 1 {
		t.Fatalf("endpoint calls = %d, want 1", calls)
	}
}

func TestOAuthTokenSourceRefreshesExpiringToken(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "token-" + string(rune('0'+calls)), "expires_in": 20})
	}))
	defer server.Close()
	source, err := NewOAuthTokenSource(server.URL, "id", "secret", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Token(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	got, err := source.Token(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if got != "token-2" || calls != 2 {
		t.Fatalf("token = %q, calls = %d", got, calls)
	}
}

func TestOAuthTokenSourceForceRefreshesToken(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "token", "expires_in": 120})
	}))
	defer server.Close()
	source, err := NewOAuthTokenSource(server.URL, "id", "secret", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Token(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Token(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("endpoint calls = %d, want 2", calls)
	}
}

func TestOAuthTokenSourceReturnsSafeError(t *testing.T) {
	secret := "super-secret-response-value"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, secret)
	}))
	defer server.Close()
	source, err := NewOAuthTokenSource(server.URL, "id", "client-secret", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = source.Token(context.Background(), false)
	if err == nil || strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "client-secret") {
		t.Fatalf("error = %v", err)
	}
}

func TestOAuthTokenSourceStatusErrorExcludesServerControlledReason(t *testing.T) {
	secret := "status-reason-secret"
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadGateway,
			Status:     "502 " + secret,
			Body:       io.NopCloser(strings.NewReader("ignored")),
			Header:     make(http.Header),
			Request:    r,
		}, nil
	})}
	source, err := NewOAuthTokenSource("https://example.com/token", "id", "secret", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	_, err = source.Token(context.Background(), false)
	if err == nil || strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), "502") {
		t.Fatalf("error = %v", err)
	}
}

func TestOAuthTokenSourceRejectsTrailingOrOversizedJSON(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "trailing JSON", body: `{"access_token":"token","expires_in":120}{"unexpected":true}`},
		{name: "trailing junk", body: `{"access_token":"token","expires_in":120} junk`},
		{name: "oversized", body: `{"access_token":"token","expires_in":120}` + strings.Repeat(" ", responseBodyLimit)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			source, err := NewOAuthTokenSource(server.URL, "id", "secret", server.Client())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := source.Token(context.Background(), false); err == nil {
				t.Fatal("Token accepted malformed or oversized JSON")
			}
		})
	}
}

func TestOAuthTokenSourceConcurrentCallersShareRefresh(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	secondStarted := make(chan struct{})
	var calls atomic.Int32
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if calls.Add(1) == 2 {
			close(secondStarted)
		}
		close(started)
		<-release
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader(`{"access_token":"shared","expires_in":120}`)), Header: make(http.Header), Request: r}, nil
	})}
	source, err := NewOAuthTokenSource("https://example.com/token", "id", "secret", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan string, 2)
	errs := make(chan error, 2)
	go func() { token, err := source.Token(context.Background(), false); results <- token; errs <- err }()
	<-started
	go func() { token, err := source.Token(context.Background(), false); results <- token; errs <- err }()
	select {
	case <-secondStarted:
		t.Fatal("started a second refresh")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
		if token := <-results; token != "shared" {
			t.Fatalf("token = %q", token)
		}
	}
}

func TestOAuthTokenSourceCancelledWaiterReturnsBeforeRefreshCompletes(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		close(started)
		<-release
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader(`{"access_token":"shared","expires_in":120}`)), Header: make(http.Header), Request: r}, nil
	})}
	source, err := NewOAuthTokenSource("https://example.com/token", "id", "secret", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	firstDone := make(chan error, 1)
	go func() { _, err := source.Token(context.Background(), false); firstDone <- err }()
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	waiterDone := make(chan error, 1)
	go func() { _, err := source.Token(ctx, false); waiterDone <- err }()
	cancel()
	select {
	case err := <-waiterDone:
		if err == nil {
			t.Fatal("cancelled waiter returned nil error")
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled waiter did not return before refresh completed")
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
}

func TestOAuthTokenSourceValidatesConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, endpoint, id, secret string
	}{
		{"empty endpoint", "", "id", "secret"},
		{"relative endpoint", "/token", "id", "secret"},
		{"ftp endpoint", "ftp://example.com/token", "id", "secret"},
		{"empty id", "https://example.com/token", "", "secret"},
		{"empty secret", "https://example.com/token", "id", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewOAuthTokenSource(tc.endpoint, tc.id, tc.secret, nil); err == nil {
				t.Fatal("expected configuration error")
			}
		})
	}
}

func TestOAuthTokenSourcePropagatesContext(t *testing.T) {
	seen := make(chan context.Context, 1)
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		seen <- r.Context()
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	source, err := NewOAuthTokenSource("https://example.com/token", "id", "secret", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := source.Token(ctx, false); done <- err }()
	<-seen
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected context cancellation error")
		}
	case <-time.After(time.Second):
		t.Fatal("request did not observe cancellation")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
