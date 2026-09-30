package products

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
)

// API is the project product lifecycle used by the Terraform resource.
type API interface {
	CreateProduct(context.Context, string, CreateProductInput, string) (string, error)
	GetProduct(context.Context, string, string) (Product, error)
	UpdateProduct(context.Context, string, string, UpdateProductInput) error
	// ArchiveProduct requests archiving. It reports whether the product is
	// archived (or absent) and, while archiving continues, the server's
	// suggested polling delay.
	ArchiveProduct(context.Context, string, string) (bool, time.Duration, error)
}

// Client keeps Publishing read and write scopes on separate authenticated transports.
type Client struct {
	write *vew.Transport
	read  *vew.Transport
}

var _ API = (*Client)(nil)

// NewClient constructs a product client from product.write and product.read transports.
func NewClient(write, read *vew.Transport) *Client {
	return &Client{write: write, read: read}
}

func productSegments(projectID, productID string, item bool) ([]string, error) {
	if strings.TrimSpace(projectID) == "" {
		return nil, errors.New("VEW project ID must not be empty")
	}
	segments := []string{"projects", projectID, "products"}
	if item {
		if strings.TrimSpace(productID) == "" {
			return nil, errors.New("VEW product ID must not be empty")
		}
		segments = append(segments, productID)
	}
	return segments, nil
}

// CreateProduct creates a product using the caller's stable idempotency key.
// The transport retries the identical marshaled body with that same key.
func (c *Client) CreateProduct(ctx context.Context, projectID string, input CreateProductInput, idempotencyKey string) (string, error) {
	segments, err := productSegments(projectID, "", false)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(input.Name) == "" {
		return "", errors.New("VEW product name must not be empty")
	}
	if !rfc4122UUID.MatchString(idempotencyKey) {
		return "", errors.New("VEW product idempotency key must be an RFC 4122 UUID")
	}
	body, err := json.Marshal(input)
	if err != nil {
		return "", errors.New("VEW product create request could not be encoded")
	}
	response, _, err := c.write.Do(ctx, http.MethodPost, segments, body, idempotencyKey)
	if err != nil {
		return "", sanitizeError(err)
	}
	var envelope struct {
		ID string `json:"productId"`
	}
	if json.Unmarshal(response, &envelope) != nil || strings.TrimSpace(envelope.ID) == "" {
		return "", errors.New("VEW product create response missing product ID")
	}
	return envelope.ID, nil
}

// GetProduct fetches one canonical product through the read-scoped transport.
func (c *Client) GetProduct(ctx context.Context, projectID, productID string) (Product, error) {
	segments, err := productSegments(projectID, productID, true)
	if err != nil {
		return Product{}, err
	}
	response, _, err := c.read.Do(ctx, http.MethodGet, segments, nil, "")
	if err != nil {
		return Product{}, sanitizeError(err)
	}
	var product Product
	if err := json.Unmarshal(response, &product); err != nil || strings.TrimSpace(product.ID) == "" {
		return Product{}, errors.New("VEW product response missing product ID")
	}
	return product, nil
}

// UpdateProduct replaces a product's mutable name and description.
func (c *Client) UpdateProduct(ctx context.Context, projectID, productID string, input UpdateProductInput) error {
	segments, err := productSegments(projectID, productID, true)
	if err != nil {
		return err
	}
	if strings.TrimSpace(input.Name) == "" {
		return errors.New("VEW product name must not be empty")
	}
	body, err := json.Marshal(input)
	if err != nil {
		return errors.New("VEW product update request could not be encoded")
	}
	_, _, err = c.write.Do(ctx, http.MethodPut, segments, body, "")
	return sanitizeError(err)
}

// ArchiveProduct archives a product, which unpublishes all of its versions.
// VEW answers 202 while archiving and 204 once archived; a missing product is
// already gone.
func (c *Client) ArchiveProduct(ctx context.Context, projectID, productID string) (bool, time.Duration, error) {
	segments, err := productSegments(projectID, productID, true)
	if err != nil {
		return false, 0, err
	}
	body, header, err := c.write.Do(ctx, http.MethodDelete, segments, nil, "")
	if vew.IsNotFound(err) {
		return true, 0, nil
	}
	if err != nil {
		return false, 0, sanitizeError(err)
	}
	// A 204 has no body; a 202 carries the product ID and a Retry-After header.
	if len(strings.TrimSpace(string(body))) == 0 {
		return true, 0, nil
	}
	return false, vew.RetryAfter(header, time.Now()), nil
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
