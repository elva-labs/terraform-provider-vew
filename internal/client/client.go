package client

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
	"uuid"
)

const clientResponseBodyLimit = 2 << 20

// Config configures the VEW API and OAuth clients.
type Config struct {
	APIURL       string
	TokenURL     string
	ClientID     string
	ClientSecret string
}

// ComponentAPI is the component API surface used by the Terraform resource.
type ComponentAPI interface {
	CreateComponent(context.Context, string, CreateComponentInput) (string, error)
	GetComponent(context.Context, string, string) (Component, error)
	UpdateComponent(context.Context, string, string, UpdateComponentInput) error
	ArchiveComponent(context.Context, string, string) error
}

// Client is an authenticated VEW component API client.
type Client struct {
	baseURL        *url.URL
	httpClient     *http.Client
	tokens         TokenSource
	maxAttempts    int
	sleep          func(context.Context, time.Duration) error
	idempotencyKey func() string
}

var _ ComponentAPI = (*Client)(nil)

// New constructs a component client from validated HTTP(S) endpoints.
func New(config Config) (*Client, error) {
	baseURL, err := parseHTTPURL(config.APIURL)
	if err != nil {
		return nil, errors.New("VEW API URL must be an absolute HTTP or HTTPS URL")
	}
	httpClient := &http.Client{Timeout: 30 * time.Second}
	tokens, err := NewOAuthTokenSource(config.TokenURL, config.ClientID, config.ClientSecret, httpClient)
	if err != nil {
		return nil, err
	}
	return &Client{
		baseURL:        baseURL,
		httpClient:     httpClient,
		tokens:         tokens,
		maxAttempts:    4,
		sleep:          sleepContext,
		idempotencyKey: func() string { return uuid.New().String() },
	}, nil
}

func parseHTTPURL(rawURL string) (*url.URL, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("invalid URL")
	}
	return u, nil
}

// CreateComponent creates a component and returns its VEW identifier.
func (c *Client) CreateComponent(ctx context.Context, projectID string, input CreateComponentInput) (string, error) {
	body, err := json.Marshal(input)
	if err != nil {
		return "", errors.New("VEW component create request could not be encoded")
	}
	endpoint, err := c.componentURL(projectID, "", false)
	if err != nil {
		return "", err
	}
	idempotencyKey := c.idempotencyKey()
	if idempotencyKey == "" {
		return "", errors.New("VEW component idempotency key could not be generated")
	}
	response, err := c.request(ctx, http.MethodPost, endpoint, body, idempotencyKey)
	if err != nil {
		return "", err
	}
	var envelope struct {
		ID string `json:"componentId"`
	}
	if err := json.Unmarshal(response, &envelope); err != nil {
		return "", errors.New("VEW component create response could not be decoded")
	}
	if envelope.ID != "" {
		return envelope.ID, nil
	}
	return "", errors.New("VEW component create response missing component ID")
}

// GetComponent fetches one component.
func (c *Client) GetComponent(ctx context.Context, projectID, componentID string) (Component, error) {
	endpoint, err := c.componentURL(projectID, componentID, true)
	if err != nil {
		return Component{}, err
	}
	response, err := c.request(ctx, http.MethodGet, endpoint, nil, "")
	if err != nil {
		return Component{}, err
	}
	var envelope struct {
		Component Component `json:"component"`
	}
	if err := json.Unmarshal(response, &envelope); err != nil {
		return Component{}, errors.New("VEW component response could not be decoded")
	}
	if envelope.Component.ID != "" {
		return envelope.Component, nil
	}
	return Component{}, errors.New("VEW component response missing component")
}

// UpdateComponent updates a component description.
func (c *Client) UpdateComponent(ctx context.Context, projectID, componentID string, input UpdateComponentInput) error {
	body, err := json.Marshal(input)
	if err != nil {
		return errors.New("VEW component update request could not be encoded")
	}
	endpoint, err := c.componentURL(projectID, componentID, true)
	if err != nil {
		return err
	}
	_, err = c.request(ctx, http.MethodPut, endpoint, body, "")
	return err
}

// ArchiveComponent archives a component. A missing component is already archived.
func (c *Client) ArchiveComponent(ctx context.Context, projectID, componentID string) error {
	endpoint, err := c.componentURL(projectID, componentID, true)
	if err != nil {
		return err
	}
	_, err = c.request(ctx, http.MethodDelete, endpoint, nil, "")
	if IsNotFound(err) {
		return nil
	}
	return err
}

func (c *Client) componentURL(projectID, componentID string, item bool) (string, error) {
	if strings.TrimSpace(projectID) == "" {
		return "", errors.New("VEW project ID must not be empty")
	}
	if item && strings.TrimSpace(componentID) == "" {
		return "", errors.New("VEW component ID must not be empty")
	}
	segments := []string{"projects", projectID, "components"}
	if item {
		segments = append(segments, componentID)
	}
	basePath := strings.TrimSuffix(c.baseURL.Path, "/")
	rawPath := strings.TrimSuffix(c.baseURL.EscapedPath(), "/")
	for _, segment := range segments {
		basePath += "/" + segment
		rawPath += "/" + escapePathSegment(segment)
	}
	u := *c.baseURL
	u.Path = basePath
	u.RawPath = rawPath
	return u.String(), nil
}

func escapePathSegment(value string) string {
	return strings.ReplaceAll(url.PathEscape(value), ".", "%2E")
}

func (c *Client) request(ctx context.Context, method, endpoint string, body []byte, idempotencyKey string) ([]byte, error) {
	forceRefresh := false
	refreshed := false
	var lastErr error
	transientAttempt := 0
	for {
		token, err := c.tokens.Token(ctx, forceRefresh)
		if err != nil {
			return nil, errors.New("VEW API authentication failed")
		}
		forceRefresh = false

		req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, errors.New("VEW API request could not be created")
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		if idempotencyKey != "" {
			req.Header.Set("Idempotency-Key", idempotencyKey)
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
			lastErr = errors.New("VEW API request failed")
			shouldRetry, retryErr := c.retry(ctx, transientAttempt, nil)
			if retryErr != nil {
				return nil, retryErr
			}
			if !shouldRetry {
				return nil, lastErr
			}
			transientAttempt++
			continue
		}
		responseBody, readErr := readResponseBody(resp.Body)
		if readErr != nil {
			lastErr = readErr
			if errors.Is(readErr, errResponseBodyTooLarge) {
				return nil, lastErr
			}
			shouldRetry, retryErr := c.retry(ctx, transientAttempt, nil)
			if retryErr != nil {
				return nil, retryErr
			}
			if !shouldRetry {
				return nil, lastErr
			}
			transientAttempt++
			continue
		}
		if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
			return responseBody, nil
		}
		apiErr := decodeAPIError(resp.StatusCode, responseBody, token)
		lastErr = apiErr
		if resp.StatusCode == http.StatusUnauthorized {
			if !refreshed {
				refreshed = true
				forceRefresh = true
				continue
			}
			return nil, lastErr
		}
		shouldRetry, retryErr := c.retry(ctx, transientAttempt, &response{header: resp.Header, status: resp.StatusCode, apiErr: apiErr})
		if retryErr != nil {
			return nil, retryErr
		}
		if !shouldRetry {
			return nil, lastErr
		}
		transientAttempt++
	}
}

type response struct {
	header http.Header
	status int
	apiErr *APIError
}

var errResponseBodyTooLarge = errors.New("VEW API response exceeds size limit")

func readResponseBody(body io.ReadCloser) ([]byte, error) {
	defer body.Close()
	limited := io.LimitReader(body, clientResponseBodyLimit+1)
	content, err := io.ReadAll(limited)
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

func (c *Client) retry(ctx context.Context, attempt int, resp *response) (bool, error) {
	if attempt+1 >= c.maxAttempts || !retryable(resp) {
		return false, nil
	}
	delay := retryDelay(resp, attempt)
	if err := c.sleep(ctx, delay); err != nil {
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

func parseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	if seconds, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && seconds >= 0 {
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
