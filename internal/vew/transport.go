package vew

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const clientResponseBodyLimit = 2 << 20

// Config configures the VEW API and OAuth clients. ProjectAPIURL and
// PublishingAPIURL are optional and are used only by Projects and Publishing
// clients; APIURL continues to target Packaging.
type Config struct {
	APIURL, ProjectAPIURL, PublishingAPIURL, TokenURL, ClientID, ClientSecret string
}

// Transport performs authenticated VEW HTTP requests.
type Transport struct {
	baseURL     *url.URL
	httpClient  *http.Client
	tokens      TokenSource
	maxAttempts int
	sleep       func(context.Context, time.Duration) error
}

// NewTransport constructs an authenticated transport from validated HTTP(S) endpoints.
func NewTransport(config Config) (*Transport, error) {
	return newTransport(config, oauthScope)
}

// NewTransportWithScopes constructs a transport whose token source requests the
// supplied scopes instead of the default component scopes.
func NewTransportWithScopes(config Config, scopes ...string) (*Transport, error) {
	if len(scopes) == 0 {
		return nil, errors.New("VEW OAuth scopes must not be empty")
	}
	for _, scope := range scopes {
		if strings.TrimSpace(scope) == "" || strings.ContainsAny(scope, " \t\r\n") {
			return nil, errors.New("VEW OAuth scope must be a nonempty token")
		}
	}
	return newTransport(config, strings.Join(scopes, " "))
}

func newTransport(config Config, scope string) (*Transport, error) {
	baseURL, err := parseHTTPURL(config.APIURL)
	if err != nil {
		return nil, errors.New("VEW API URL must be an absolute HTTP or HTTPS URL")
	}
	httpClient := &http.Client{Timeout: 30 * time.Second}
	tokens, err := newOAuthTokenSource(config.TokenURL, config.ClientID, config.ClientSecret, scope, httpClient)
	if err != nil {
		return nil, err
	}
	return &Transport{baseURL: baseURL, httpClient: httpClient, tokens: tokens, maxAttempts: 4, sleep: sleepContext}, nil
}

func parseHTTPURL(rawURL string) (*url.URL, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("invalid URL")
	}
	return u, nil
}

// Do sends a VEW request to a path made solely from separately escaped segments.
// It returns response headers so domain clients can inspect protocol metadata.
func (t *Transport) Do(ctx context.Context, method string, pathSegments []string, body []byte, idempotencyKey string) ([]byte, http.Header, error) {
	endpoint := t.url(pathSegments)
	forceRefresh, refreshed, transientAttempt := false, false, 0
	var lastErr error
	for {
		token, err := t.tokens.Token(ctx, forceRefresh)
		if err != nil {
			return nil, nil, errors.New("VEW API authentication failed")
		}
		forceRefresh = false
		req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, nil, errors.New("VEW API request could not be created")
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		if idempotencyKey != "" {
			req.Header.Set("Idempotency-Key", idempotencyKey)
		}
		resp, err := t.httpClient.Do(req)
		if err != nil {
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
			lastErr = errors.New("VEW API request failed")
			shouldRetry, retryErr := t.retry(ctx, transientAttempt, nil)
			if retryErr != nil {
				return nil, nil, retryErr
			}
			if !shouldRetry {
				return nil, nil, lastErr
			}
			transientAttempt++
			continue
		}
		responseBody, readErr := readResponseBody(resp.Body)
		if readErr != nil {
			lastErr = readErr
			if errors.Is(readErr, errResponseBodyTooLarge) {
				return nil, resp.Header, lastErr
			}
			shouldRetry, retryErr := t.retry(ctx, transientAttempt, nil)
			if retryErr != nil {
				return nil, resp.Header, retryErr
			}
			if !shouldRetry {
				return nil, resp.Header, lastErr
			}
			transientAttempt++
			continue
		}
		if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
			return responseBody, resp.Header, nil
		}
		apiErr := decodeAPIError(resp.StatusCode, responseBody, token)
		lastErr = apiErr
		if resp.StatusCode == http.StatusUnauthorized {
			if !refreshed {
				refreshed, forceRefresh = true, true
				continue
			}
			return nil, resp.Header, lastErr
		}
		shouldRetry, retryErr := t.retry(ctx, transientAttempt, &response{header: resp.Header, status: resp.StatusCode, apiErr: apiErr})
		if retryErr != nil {
			return nil, resp.Header, retryErr
		}
		if !shouldRetry {
			return nil, resp.Header, lastErr
		}
		transientAttempt++
	}
}

func (t *Transport) url(pathSegments []string) string {
	basePath, rawPath := strings.TrimSuffix(t.baseURL.Path, "/"), strings.TrimSuffix(t.baseURL.EscapedPath(), "/")
	for _, segment := range pathSegments {
		basePath += "/" + segment
		rawPath += "/" + escapePathSegment(segment)
	}
	u := *t.baseURL
	u.Path, u.RawPath = basePath, rawPath
	return u.String()
}
func escapePathSegment(value string) string {
	return strings.ReplaceAll(url.PathEscape(value), ".", "%2E")
}

type response struct {
	header http.Header
	status int
	apiErr *APIError
}

var errResponseBodyTooLarge = errors.New("VEW API response exceeds size limit")

func readResponseBody(body io.ReadCloser) ([]byte, error) {
	defer body.Close()
	content, err := io.ReadAll(io.LimitReader(body, clientResponseBodyLimit+1))
	if err != nil {
		return nil, errors.New("VEW API response could not be read")
	}
	if len(content) > clientResponseBodyLimit {
		return nil, errResponseBodyTooLarge
	}
	return content, nil
}
func decodeAPIError(status int, body []byte, authorizationValues ...string) *APIError {
	var problem Problem
	_ = json.Unmarshal(body, &problem)
	for _, value := range authorizationValues {
		if value == "" {
			continue
		}
		problem.Type = strings.ReplaceAll(problem.Type, value, "[redacted]")
		problem.Title = strings.ReplaceAll(problem.Title, value, "[redacted]")
		problem.Detail = strings.ReplaceAll(problem.Detail, value, "[redacted]")
		problem.Code = strings.ReplaceAll(problem.Code, value, "[redacted]")
		problem.RequestID = strings.ReplaceAll(problem.RequestID, value, "[redacted]")
	}
	return fmtAPIError(status, problem)
}
func (t *Transport) retry(ctx context.Context, attempt int, resp *response) (bool, error) {
	if attempt+1 >= t.maxAttempts || !retryable(resp) {
		return false, nil
	}
	if err := t.sleep(ctx, retryDelay(resp, attempt)); err != nil {
		return false, err
	}
	return true, nil
}
func retryable(resp *response) bool {
	if resp == nil {
		return true
	}
	if resp.status == http.StatusConflict {
		return resp.apiErr.Problem.Code == "IDEMPOTENCY_REQUEST_IN_PROGRESS"
	}
	return resp.status == http.StatusTooManyRequests || resp.status >= http.StatusInternalServerError || resp.apiErr.Problem.Retryable
}
func retryDelay(resp *response, attempt int) time.Duration {
	if resp != nil {
		if delay, ok := parseRetryAfter(resp.header.Get("Retry-After"), time.Now()); ok {
			return delay
		}
	}
	base := 250 * time.Millisecond
	if attempt == 1 {
		base = 500 * time.Millisecond
	} else if attempt >= 2 {
		base = time.Second
	}
	return base + time.Duration(rand.Float64()*0.25*float64(base))
}

// RetryAfter returns the server-provided delay from a Retry-After header.
// It accepts delta-seconds and HTTP dates, returning zero when no future delay
// was supplied. It is intentionally separate from retryDelay so asynchronous
// domain actions can hand the server's polling advice to a waiter.
func RetryAfter(header http.Header, now time.Time) time.Duration {
	delay, ok := parseRetryAfter(header.Get("Retry-After"), now)
	if !ok {
		return 0
	}
	return delay
}

func parseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	if seconds, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && seconds >= 0 {
		const maxDurationSeconds = int64(1<<63-1) / int64(time.Second)
		if int64(seconds) > maxDurationSeconds {
			return 0, false
		}
		return time.Duration(seconds) * time.Second, true
	}
	if when, err := http.ParseTime(value); err == nil {
		delay := when.Sub(now)
		if delay < 0 {
			delay = 0
		}
		return delay, true
	}
	return 0, false
}
func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
