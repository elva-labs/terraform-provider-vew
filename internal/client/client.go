package client

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
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
		idempotencyKey: newUUID,
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
	response, err := c.request(ctx, http.MethodPost, c.componentURL(projectID, ""), body, c.idempotencyKey())
	if err != nil {
		return "", err
	}
	var envelope struct {
		ID        string    `json:"id"`
		Component Component `json:"component"`
		Data      Component `json:"data"`
	}
	if err := json.Unmarshal(response, &envelope); err != nil {
		return "", errors.New("VEW component create response could not be decoded")
	}
	if envelope.ID != "" {
		return envelope.ID, nil
	}
	if envelope.Component.ID != "" {
		return envelope.Component.ID, nil
	}
	if envelope.Data.ID != "" {
		return envelope.Data.ID, nil
	}
	return "", errors.New("VEW component create response missing component ID")
}

// GetComponent fetches one component.
func (c *Client) GetComponent(ctx context.Context, projectID, componentID string) (Component, error) {
	response, err := c.request(ctx, http.MethodGet, c.componentURL(projectID, componentID), nil, "")
	if err != nil {
		return Component{}, err
	}
	var envelope struct {
		Component Component `json:"component"`
		Data      Component `json:"data"`
	}
	if err := json.Unmarshal(response, &envelope); err != nil {
		return Component{}, errors.New("VEW component response could not be decoded")
	}
	if envelope.Component.ID != "" {
		return envelope.Component, nil
	}
	if envelope.Data.ID != "" {
		return envelope.Data, nil
	}
	return Component{}, errors.New("VEW component response missing component")
}

// UpdateComponent updates a component description.
func (c *Client) UpdateComponent(ctx context.Context, projectID, componentID string, input UpdateComponentInput) error {
	body, err := json.Marshal(input)
	if err != nil {
		return errors.New("VEW component update request could not be encoded")
	}
	_, err = c.request(ctx, http.MethodPut, c.componentURL(projectID, componentID), body, "")
	return err
}

// ArchiveComponent archives a component. A missing component is already archived.
func (c *Client) ArchiveComponent(ctx context.Context, projectID, componentID string) error {
	_, err := c.request(ctx, http.MethodDelete, c.componentURL(projectID, componentID), nil, "")
	if IsNotFound(err) {
		return nil
	}
	return err
}

func (c *Client) componentURL(projectID, componentID string) string {
	parts := []string{"projects", projectID, "components"}
	if componentID != "" {
		parts = append(parts, componentID)
	}
	u := c.baseURL.JoinPath(parts...)
	return u.String()
}

func (c *Client) request(ctx context.Context, method, endpoint string, body []byte, idempotencyKey string) ([]byte, error) {
	forceRefresh := false
	refreshed := false
	var lastErr error
	for attempt := 0; attempt < c.maxAttempts; attempt++ {
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
			shouldRetry, retryErr := c.retry(ctx, attempt, nil)
			if retryErr != nil {
				return nil, retryErr
			}
			if !shouldRetry {
				return nil, lastErr
			}
			continue
		}
		responseBody, readErr := readResponseBody(resp.Body)
		if readErr != nil {
			lastErr = readErr
			shouldRetry, retryErr := c.retry(ctx, attempt, nil)
			if retryErr != nil {
				return nil, retryErr
			}
			if !shouldRetry {
				return nil, lastErr
			}
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
		shouldRetry, retryErr := c.retry(ctx, attempt, &response{header: resp.Header, status: resp.StatusCode, apiErr: apiErr})
		if retryErr != nil {
			return nil, retryErr
		}
		if !shouldRetry {
			return nil, lastErr
		}
	}
	return nil, lastErr
}

type response struct {
	header http.Header
	status int
	apiErr *APIError
}

func readResponseBody(body io.ReadCloser) ([]byte, error) {
	defer body.Close()
	limited := io.LimitReader(body, clientResponseBodyLimit+1)
	content, err := io.ReadAll(limited)
	if err != nil {
		return nil, errors.New("VEW API response could not be read")
	}
	if len(content) > clientResponseBodyLimit {
		return nil, errors.New("VEW API response exceeds size limit")
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
		return resp.apiErr.Problem.Code == "IDEMPOTENCY_IN_PROGRESS"
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

func newUUID() string {
	var value [16]byte
	if _, err := cryptorand.Read(value[:]); err != nil {
		return ""
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	encoded := make([]byte, 36)
	hex.Encode(encoded[0:8], value[0:4])
	encoded[8] = '-'
	hex.Encode(encoded[9:13], value[4:6])
	encoded[13] = '-'
	hex.Encode(encoded[14:18], value[6:8])
	encoded[18] = '-'
	hex.Encode(encoded[19:23], value[8:10])
	encoded[23] = '-'
	hex.Encode(encoded[24:36], value[10:16])
	return string(encoded)
}
