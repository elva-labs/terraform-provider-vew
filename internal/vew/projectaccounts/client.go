package projectaccounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
)

// API is the Terraform project-account operation surface.
type API interface {
	CreateAccount(context.Context, string, AccountInput, string) (ActionResult, error)
	GetAccount(context.Context, string, string) (Account, error)
	UpdateAccount(context.Context, string, string, UpdateAccountInput) (ActionResult, error)
	DeactivateAccount(context.Context, string, string) error
}

// Client keeps account reads and writes on separately scoped OAuth transports.
type Client struct {
	write *vew.Transport
	read  *vew.Transport
}

var _ API = (*Client)(nil)

func NewClient(write, read *vew.Transport) *Client {
	return &Client{write: write, read: read}
}

func accountSegments(projectID, accountID string, item bool) ([]string, error) {
	if strings.TrimSpace(projectID) == "" {
		return nil, errors.New("VEW project ID must not be empty")
	}
	segments := []string{"projects", projectID, "accounts"}
	if item {
		if strings.TrimSpace(accountID) == "" {
			return nil, errors.New("VEW project account ID must not be empty")
		}
		segments = append(segments, accountID)
	}
	return segments, nil
}

func (c *Client) CreateAccount(ctx context.Context, projectID string, input AccountInput, idempotencyKey string) (ActionResult, error) {
	segments, err := accountSegments(projectID, "", false)
	if err != nil {
		return ActionResult{}, err
	}
	if !rfc4122UUID.MatchString(idempotencyKey) {
		return ActionResult{}, errors.New("VEW project account idempotency key must be an RFC 4122 UUID")
	}
	body, err := json.Marshal(input)
	if err != nil {
		return ActionResult{}, errors.New("VEW project account create request could not be encoded")
	}
	response, headers, err := c.write.Do(ctx, http.MethodPost, segments, body, idempotencyKey)
	if err != nil {
		return ActionResult{}, safeAccountError("create", err)
	}
	var envelope struct {
		ID string `json:"accountId"`
	}
	if json.Unmarshal(response, &envelope) != nil {
		return ActionResult{}, errors.New("VEW project account create response could not be decoded")
	}
	if strings.TrimSpace(envelope.ID) == "" {
		return ActionResult{}, errors.New("VEW project account create response missing account ID")
	}
	return ActionResult{ID: envelope.ID, RetryAfter: vew.RetryAfter(headers, time.Now())}, nil
}

func (c *Client) GetAccount(ctx context.Context, projectID, accountID string) (Account, error) {
	segments, err := accountSegments(projectID, accountID, true)
	if err != nil {
		return Account{}, err
	}
	response, headers, err := c.read.Do(ctx, http.MethodGet, segments, nil, "")
	if err != nil {
		return Account{}, safeAccountError("read", err)
	}
	var account Account
	if json.Unmarshal(response, &account) != nil {
		return Account{}, errors.New("VEW project account response could not be decoded")
	}
	if strings.TrimSpace(account.ID) == "" {
		return Account{}, errors.New("VEW project account response missing account")
	}
	account.RetryAfter = vew.RetryAfter(headers, time.Now())
	return account, nil
}

func (c *Client) UpdateAccount(ctx context.Context, projectID, accountID string, input UpdateAccountInput) (ActionResult, error) {
	segments, err := accountSegments(projectID, accountID, true)
	if err != nil {
		return ActionResult{}, err
	}
	body, err := json.Marshal(input)
	if err != nil {
		return ActionResult{}, errors.New("VEW project account update request could not be encoded")
	}
	response, headers, err := c.write.Do(ctx, http.MethodPut, segments, body, "")
	if err != nil {
		return ActionResult{}, safeAccountError("update", err)
	}
	var envelope struct {
		ID string `json:"accountId"`
	}
	if json.Unmarshal(response, &envelope) != nil {
		return ActionResult{}, errors.New("VEW project account update response could not be decoded")
	}
	if strings.TrimSpace(envelope.ID) == "" {
		return ActionResult{}, errors.New("VEW project account update response missing account ID")
	}
	if envelope.ID != accountID {
		return ActionResult{}, errors.New("VEW project account update response returned a different account ID")
	}
	return ActionResult{ID: envelope.ID, RetryAfter: vew.RetryAfter(headers, time.Now())}, nil
}

func (c *Client) DeactivateAccount(ctx context.Context, projectID, accountID string) error {
	segments, err := accountSegments(projectID, accountID, true)
	if err != nil {
		return err
	}
	_, _, err = c.write.Do(ctx, http.MethodDelete, segments, nil, "")
	if vew.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return safeAccountError("deactivate", err)
	}
	return nil
}

var safeToken = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,80}$`)
var rfc4122UUID = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// safeAPIError retains errors.As support for status and conflict handling while
// keeping arbitrary backend problem details out of diagnostics.
type safeAPIError struct {
	status    int
	code      string
	requestID string
	retryable bool
}

func safeAccountError(operation string, err error) error {
	if err == nil {
		return nil
	}
	var apiErr *vew.APIError
	if errors.As(err, &apiErr) {
		wrapped := &safeAPIError{status: apiErr.Status, retryable: apiErr.Problem.Retryable}
		if safeToken.MatchString(apiErr.Problem.Code) {
			wrapped.code = apiErr.Problem.Code
		}
		if safeToken.MatchString(apiErr.Problem.RequestID) {
			wrapped.requestID = apiErr.Problem.RequestID
		}
		return fmt.Errorf("VEW project account %s failed: %w", operation, wrapped)
	}
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("VEW project account %s failed: %w", operation, context.Canceled)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("VEW project account %s failed: %w", operation, context.DeadlineExceeded)
	}
	return fmt.Errorf("VEW project account %s failed", operation)
}

func (e *safeAPIError) Error() string {
	message := fmt.Sprintf("HTTP %d", e.status)
	if e.code != "" {
		message += " (" + e.code + ")"
	}
	if e.requestID != "" {
		message += " request ID " + e.requestID
	}
	return message
}

func (e *safeAPIError) As(target any) bool {
	apiErr, ok := target.(**vew.APIError)
	if !ok {
		return false
	}
	*apiErr = &vew.APIError{
		Status: e.status,
		Problem: vew.Problem{
			Status: e.status, Code: e.code, RequestID: e.requestID, Retryable: e.retryable,
		},
	}
	return true
}
