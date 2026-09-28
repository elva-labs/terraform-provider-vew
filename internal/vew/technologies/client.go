package technologies

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
)

// API is the synchronous project technology lifecycle used by the Terraform resource.
type API interface {
	CreateTechnology(context.Context, string, TechnologyInput, string) (string, error)
	GetTechnology(context.Context, string, string) (Technology, error)
	UpdateTechnology(context.Context, string, string, TechnologyInput) error
	DeleteTechnology(context.Context, string, string) error
}

// Client keeps Projects read and write scopes on separate authenticated transports.
type Client struct {
	write *vew.Transport
	read  *vew.Transport
}

var _ API = (*Client)(nil)

// NewClient constructs a technology client from technology.write and technology.read transports.
func NewClient(write, read *vew.Transport) *Client {
	return &Client{write: write, read: read}
}

func technologySegments(projectID, technologyID string, item bool) ([]string, error) {
	if strings.TrimSpace(projectID) == "" {
		return nil, errors.New("VEW project ID must not be empty")
	}
	segments := []string{"projects", projectID, "technologies"}
	if item {
		if strings.TrimSpace(technologyID) == "" {
			return nil, errors.New("VEW technology ID must not be empty")
		}
		segments = append(segments, technologyID)
	}
	return segments, nil
}

// CreateTechnology creates a technology using the caller's stable idempotency key.
// The transport retries the identical marshaled body with that same key.
func (c *Client) CreateTechnology(ctx context.Context, projectID string, input TechnologyInput, idempotencyKey string) (string, error) {
	segments, err := technologySegments(projectID, "", false)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(input.Name) == "" {
		return "", errors.New("VEW technology name must not be empty")
	}
	if !rfc4122UUID.MatchString(idempotencyKey) {
		return "", errors.New("VEW technology idempotency key must be an RFC 4122 UUID")
	}
	body, err := json.Marshal(input)
	if err != nil {
		return "", errors.New("VEW technology create request could not be encoded")
	}
	response, _, err := c.write.Do(ctx, http.MethodPost, segments, body, idempotencyKey)
	if err != nil {
		return "", sanitizeError(err)
	}
	var envelope struct {
		ID string `json:"technologyId"`
	}
	if json.Unmarshal(response, &envelope) != nil || strings.TrimSpace(envelope.ID) == "" {
		return "", errors.New("VEW technology create response missing technology ID")
	}
	return envelope.ID, nil
}

// GetTechnology fetches one canonical technology through the read-scoped transport.
func (c *Client) GetTechnology(ctx context.Context, projectID, technologyID string) (Technology, error) {
	segments, err := technologySegments(projectID, technologyID, true)
	if err != nil {
		return Technology{}, err
	}
	response, _, err := c.read.Do(ctx, http.MethodGet, segments, nil, "")
	if err != nil {
		return Technology{}, sanitizeError(err)
	}
	technology, err := decodeTechnology(response)
	if err != nil {
		return Technology{}, err
	}
	return technology, nil
}

// UpdateTechnology replaces a technology's mutable name and description.
func (c *Client) UpdateTechnology(ctx context.Context, projectID, technologyID string, input TechnologyInput) error {
	segments, err := technologySegments(projectID, technologyID, true)
	if err != nil {
		return err
	}
	if strings.TrimSpace(input.Name) == "" {
		return errors.New("VEW technology name must not be empty")
	}
	body, err := json.Marshal(input)
	if err != nil {
		return errors.New("VEW technology update request could not be encoded")
	}
	_, _, err = c.write.Do(ctx, http.MethodPut, segments, body, "")
	if err != nil {
		return sanitizeError(err)
	}
	return nil
}

// DeleteTechnology removes a technology. A missing technology is already deleted.
func (c *Client) DeleteTechnology(ctx context.Context, projectID, technologyID string) error {
	segments, err := technologySegments(projectID, technologyID, true)
	if err != nil {
		return err
	}
	_, _, err = c.write.Do(ctx, http.MethodDelete, segments, nil, "")
	if vew.IsNotFound(err) {
		return nil
	}
	return sanitizeError(err)
}

func decodeTechnology(body []byte) (Technology, error) {
	var envelope struct {
		Technology *Technology `json:"technology"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return Technology{}, errors.New("VEW technology response could not be decoded")
	}
	if envelope.Technology != nil {
		if strings.TrimSpace(envelope.Technology.ID) == "" {
			return Technology{}, errors.New("VEW technology response missing technology ID")
		}
		return *envelope.Technology, nil
	}
	var technology Technology
	if err := json.Unmarshal(body, &technology); err != nil || strings.TrimSpace(technology.ID) == "" {
		return Technology{}, errors.New("VEW technology response missing technology ID")
	}
	return technology, nil
}

var safeProblemValue = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
var rfc4122UUID = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// sanitizeError preserves safe API correlation fields while dropping arbitrary
// titles, details, and response content before callers can use Error().
func sanitizeError(err error) error {
	if err == nil {
		return nil
	}
	var apiErr *vew.APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	problem := vew.Problem{Status: apiErr.Status, Retryable: apiErr.Problem.Retryable}
	if safeProblemValue.MatchString(apiErr.Problem.Code) {
		problem.Code = apiErr.Problem.Code
	}
	if safeProblemValue.MatchString(apiErr.Problem.RequestID) {
		problem.RequestID = apiErr.Problem.RequestID
	}
	return &vew.APIError{Status: apiErr.Status, Problem: problem}
}
