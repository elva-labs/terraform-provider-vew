package vew

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestTransportScopesKeepComponentAndRecipeTokensSeparate(t *testing.T) {
	var requested []string
	var authorized []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			if err := r.ParseForm(); err != nil {
				t.Error(err)
				return
			}
			scope := r.Form.Get("scope")
			requested = append(requested, scope)
			_, _ = io.WriteString(w, `{"access_token":"token-`+strconv.Itoa(len(requested))+`","expires_in":3600}`)
			return
		}
		authorized = append(authorized, r.Header.Get("Authorization"))
		_, _ = io.WriteString(w, `{}`)
	}))
	defer server.Close()
	config := Config{APIURL: server.URL, TokenURL: server.URL + "/oauth/token", ClientID: "id", ClientSecret: "secret"}
	component, err := NewTransport(config)
	if err != nil {
		t.Fatal(err)
	}
	recipe, err := NewTransportWithScopes(config, "clients/packaging/recipe.read", "clients/packaging/recipe.write")
	if err != nil {
		t.Fatal(err)
	}
	for _, transport := range []*Transport{component, recipe, component, recipe} {
		if _, _, err := transport.Do(context.Background(), http.MethodGet, []string{"projects", "project"}, nil, ""); err != nil {
			t.Fatal(err)
		}
	}
	if len(requested) != 2 || requested[0] != oauthScope || requested[1] != "clients/packaging/recipe.read clients/packaging/recipe.write" {
		t.Fatalf("requested scopes = %q", requested)
	}
	if strings.Join(authorized, ",") != "Bearer token-1,Bearer token-2,Bearer token-1,Bearer token-2" {
		t.Fatalf("API authorizations = %q", authorized)
	}
}

func TestTransportScopesRejectInvalidInput(t *testing.T) {
	config := Config{APIURL: "https://vew.example", TokenURL: "https://token.example", ClientID: "id", ClientSecret: "secret"}
	for _, scopes := range [][]string{nil, {""}, {"valid", "two words"}} {
		if _, err := NewTransportWithScopes(config, scopes...); err == nil {
			t.Fatalf("expected invalid scopes %q to fail", scopes)
		}
	}
}

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

type staticTokenSource struct{ token string }

func (s staticTokenSource) Token(context.Context, bool) (string, error) {
	if s.token != "" {
		return s.token, nil
	}
	return "token", nil
}

// transportDo keeps tests explicit about the public Transport boundary.
func transportDo(ctx context.Context, transport *Transport, method string, pathSegments []string, body []byte, idempotencyKey string) ([]byte, http.Header, error) {
	return transport.Do(ctx, method, pathSegments, body, idempotencyKey)
}

func TestTransportRefreshesTokenOnceAfterUnauthorized(t *testing.T) {
	tokens := &transportTokenSource{values: []string{"old", "new"}}
	calls := 0
	transport := testTransport(t, "https://vew.example", &http.Client{Transport: transportRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.Header.Get("Authorization") == "Bearer old" {
			return transportResponse(request, http.StatusUnauthorized, ``), nil
		}
		if request.Header.Get("Authorization") != "Bearer new" {
			t.Fatalf("authorization = %q", request.Header.Get("Authorization"))
		}
		return transportResponse(request, http.StatusOK, `{}`), nil
	})}, tokens)
	if _, _, err := transport.Do(context.Background(), http.MethodGet, []string{"projects", "proj", "components", "cmp"}, nil, ""); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || strings.Join(tokens.forces, ",") != "false,true" {
		t.Fatalf("calls/forces = %d/%v", calls, tokens.forces)
	}
}

func TestTransportDoesNotRetryUnauthorizedAfterForcedRefresh(t *testing.T) {
	tokens := &transportTokenSource{values: []string{"old", "new"}}
	calls := 0
	transport := testTransport(t, "https://vew.example", &http.Client{Transport: transportRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		return transportResponse(request, http.StatusUnauthorized, `{"retryable":true}`), nil
	})}, tokens)
	transport.sleep = func(context.Context, time.Duration) error { return nil }
	if _, _, err := transport.Do(context.Background(), http.MethodGet, []string{"projects", "proj"}, nil, ""); err == nil || calls != 2 || strings.Join(tokens.forces, ",") != "false,true" {
		t.Fatalf("error/calls/forces = %v/%d/%v", err, calls, tokens.forces)
	}
}

func TestTransportReusesIdempotencyKeyAndBodyAfterLostResponse(t *testing.T) {
	var bodies [][]byte
	var keys []string
	transport := testTransport(t, "https://vew.example", &http.Client{Transport: transportRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		bodies, keys = append(bodies, body), append(keys, request.Header.Get("Idempotency-Key"))
		if len(bodies) == 1 {
			return nil, io.ErrUnexpectedEOF
		}
		return transportResponse(request, http.StatusCreated, `{}`), nil
	})}, staticTokenSource{})
	transport.sleep = func(context.Context, time.Duration) error { return nil }
	if _, _, err := transport.Do(context.Background(), http.MethodPost, []string{"projects", "proj", "components"}, []byte(`{"componentName":"n"}`), "stable-key"); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 || keys[0] != "stable-key" || keys[0] != keys[1] || !bytes.Equal(bodies[0], bodies[1]) {
		t.Fatalf("calls/keys/bodies = %d/%q/%q", len(bodies), keys, bodies)
	}
}

func TestTransportHonorsHTTPDateRetryAfter(t *testing.T) {
	calls := 0
	var delay time.Duration
	transport := testTransport(t, "https://vew.example", &http.Client{Transport: transportRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			response := transportResponse(request, http.StatusTooManyRequests, ``)
			response.Header.Set("Retry-After", time.Now().UTC().Add(10*time.Second).Format(http.TimeFormat))
			return response, nil
		}
		return transportResponse(request, http.StatusOK, `{}`), nil
	})}, staticTokenSource{})
	transport.sleep = func(_ context.Context, got time.Duration) error { delay = got; return nil }
	if _, _, err := transport.Do(context.Background(), http.MethodGet, []string{"projects", "proj"}, nil, ""); err != nil {
		t.Fatal(err)
	}
	if delay < 8*time.Second || delay > 10*time.Second {
		t.Fatalf("delay = %v, want HTTP-date delay", delay)
	}
}

func TestRetryAfterReturnsDeltaSeconds(t *testing.T) {
	if got := RetryAfter(http.Header{"Retry-After": []string{"4"}}, time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)); got != 4*time.Second {
		t.Fatalf("RetryAfter = %v, want 4s", got)
	}
}

func TestRetryAfterReturnsZeroForOverflowingDeltaSeconds(t *testing.T) {
	if got := RetryAfter(http.Header{"Retry-After": []string{"9223372037"}}, time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)); got != 0 {
		t.Fatalf("RetryAfter = %v, want 0", got)
	}
}

func TestRetryAfterReturnsHTTPDateDelay(t *testing.T) {
	now := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
	header := http.Header{"Retry-After": []string{now.Add(7 * time.Second).Format(http.TimeFormat)}}
	if got := RetryAfter(header, now); got != 7*time.Second {
		t.Fatalf("RetryAfter = %v, want 7s", got)
	}
}

func TestRetryAfterReturnsZeroForAbsentMalformedAndPastValues(t *testing.T) {
	now := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
	for _, header := range []http.Header{nil, {"Retry-After": []string{"invalid"}}, {"Retry-After": []string{now.Add(-time.Second).Format(http.TimeFormat)}}} {
		if got := RetryAfter(header, now); got != 0 {
			t.Fatalf("RetryAfter(%v) = %v, want 0", header, got)
		}
	}
}

func TestTransportRedactsBearerTokenFromProblem(t *testing.T) {
	const token = "access-token-value"
	transport := testTransport(t, "https://vew.example", &http.Client{Transport: transportRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return transportResponse(request, http.StatusBadRequest, `{"detail":"authentication failed for access-token-value"}`), nil
	})}, staticTokenSource{token: token})
	_, _, err := transport.Do(context.Background(), http.MethodGet, []string{"projects", "proj"}, nil, "")
	if err == nil || strings.Contains(err.Error(), token) {
		t.Fatalf("error exposed bearer token: %v", err)
	}
}

func TestTransportStopsRetrySleepWhenContextIsCancelled(t *testing.T) {
	transport := testTransport(t, "https://vew.example", &http.Client{Transport: transportRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return transportResponse(request, http.StatusServiceUnavailable, ``), nil
	})}, staticTokenSource{})
	transport.sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := transport.Do(ctx, http.MethodGet, []string{"projects", "proj"}, nil, "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context cancellation", err)
	}
}

func TestTransportStopsAfterMaximumRetryAttempts(t *testing.T) {
	calls := 0
	transport := testTransport(t, "https://vew.example", &http.Client{Transport: transportRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		return transportResponse(request, http.StatusServiceUnavailable, ``), nil
	})}, staticTokenSource{})
	transport.maxAttempts = 3
	transport.sleep = func(context.Context, time.Duration) error { return nil }
	if _, _, err := transport.Do(context.Background(), http.MethodGet, []string{"projects", "proj"}, nil, ""); err == nil || calls != 3 {
		t.Fatalf("error/calls = %v/%d", err, calls)
	}
}

func TestTransportClosesEachRetryResponseBody(t *testing.T) {
	var bodies []*trackingReadCloser
	calls := 0
	transport := testTransport(t, "https://vew.example", &http.Client{Transport: transportRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		body := &trackingReadCloser{Reader: strings.NewReader(`{}`)}
		bodies = append(bodies, body)
		status := http.StatusServiceUnavailable
		if calls == 2 {
			status = http.StatusOK
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: body, Request: request}, nil
	})}, staticTokenSource{})
	transport.sleep = func(context.Context, time.Duration) error { return nil }
	if _, _, err := transport.Do(context.Background(), http.MethodGet, []string{"projects", "proj"}, nil, ""); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 || !bodies[0].closed || !bodies[1].closed {
		t.Fatal("response bodies were not all closed")
	}
}

func TestTransportLimitsResponseBodyReads(t *testing.T) {
	calls := 0
	transport := testTransport(t, "https://vew.example", &http.Client{Transport: transportRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		return transportResponse(request, http.StatusOK, strings.Repeat("x", clientResponseBodyLimit+1)), nil
	})}, staticTokenSource{})
	_, _, err := transport.Do(context.Background(), http.MethodGet, []string{"projects", "proj"}, nil, "")
	if err == nil || !strings.Contains(err.Error(), "size limit") || calls != 1 {
		t.Fatalf("error/calls = %v/%d", err, calls)
	}
}

func TestAPIErrorNotFound(t *testing.T) {
	err := fmtAPIError(http.StatusNotFound, Problem{Detail: "missing"})
	if !IsNotFound(err) || IsNotFound(errors.New("missing")) {
		t.Fatalf("IsNotFound = %v", IsNotFound(err))
	}
}

func testTransport(t *testing.T, rawURL string, httpClient *http.Client, tokens TokenSource) *Transport {
	t.Helper()
	baseURL, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	return &Transport{baseURL: baseURL, httpClient: httpClient, tokens: tokens, maxAttempts: 4, sleep: sleepContext}
}

type transportTokenSource struct {
	values []string
	forces []string
}

func (s *transportTokenSource) Token(_ context.Context, force bool) (string, error) {
	s.forces = append(s.forces, strconv.FormatBool(force))
	if len(s.values) == 0 {
		return "token", nil
	}
	token := s.values[0]
	s.values = s.values[1:]
	return token, nil
}
func transportResponse(request *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}
}

type trackingReadCloser struct {
	io.Reader
	closed bool
}

func (r *trackingReadCloser) Close() error { r.closed = true; return nil }
