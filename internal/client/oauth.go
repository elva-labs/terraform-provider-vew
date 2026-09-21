package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	oauthScope        = "clients/packaging/component.read clients/packaging/component.write"
	responseBodyLimit = 1 << 20
	expirySkew        = 30 * time.Second
)

// TokenSource obtains an OAuth access token. forceRefresh bypasses any cached token.
type TokenSource interface {
	Token(context.Context, bool) (string, error)
}

// OAuthTokenSource implements the OAuth 2.0 client-credentials grant.
type OAuthTokenSource struct {
	tokenURL     string
	clientID     string
	clientSecret string
	httpClient   *http.Client
	now          func() time.Time

	mu       sync.Mutex
	token    string
	tokenExp time.Time
}

// NewOAuthTokenSource creates a token source for an absolute HTTP(S) token URL.
func NewOAuthTokenSource(tokenURL, clientID, clientSecret string, httpClient *http.Client) (*OAuthTokenSource, error) {
	if strings.TrimSpace(clientID) == "" {
		return nil, errors.New("oauth client ID must not be empty")
	}
	if strings.TrimSpace(clientSecret) == "" {
		return nil, errors.New("oauth client secret must not be empty")
	}
	u, err := url.Parse(tokenURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("oauth token URL must be an absolute HTTP or HTTPS URL")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &OAuthTokenSource{
		tokenURL:     tokenURL,
		clientID:     clientID,
		clientSecret: clientSecret,
		httpClient:   httpClient,
		now:          time.Now,
	}, nil
}

type oauthTokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int64  `json:"expires_in"`
}

// Token returns a cached token when it has more than 30 seconds remaining;
// otherwise it obtains and caches a fresh token.
func (s *OAuthTokenSource) Token(ctx context.Context, forceRefresh bool) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !forceRefresh && s.token != "" && s.tokenExp.Sub(s.now()) > expirySkew {
		return s.token, nil
	}

	form := url.Values{
		"grant_type": {"client_credentials"},
		"scope":      {oauthScope},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", errors.New("oauth token request could not be created")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(s.clientID, s.clientSecret)
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", errors.New("oauth token request failed")
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("oauth token endpoint returned status %s", resp.Status)
	}
	var token oauthTokenResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, responseBodyLimit)).Decode(&token); err != nil {
		return "", fmt.Errorf("oauth token response decoding failed: %w", err)
	}
	if token.AccessToken == "" {
		return "", errors.New("oauth token response missing access_token")
	}
	if token.ExpiresIn <= 0 {
		return "", errors.New("oauth token response has invalid expires_in")
	}

	s.token = token.AccessToken
	s.tokenExp = s.now().Add(time.Duration(token.ExpiresIn) * time.Second)
	return s.token, nil
}
