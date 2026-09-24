package components

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
)

// ActionResult identifies an accepted asynchronous component-version action.
type ActionResult struct {
	ID         string
	RetryAfter time.Duration
}

// Dependency identifies a component version required by another version.
type Dependency struct {
	ComponentID   string  `json:"componentId"`
	ComponentName string  `json:"componentName"`
	VersionID     string  `json:"componentVersionId"`
	VersionName   string  `json:"componentVersionName"`
	Type          string  `json:"componentVersionType"`
	Order         int64   `json:"order"`
	Position      *string `json:"position,omitempty"`
}

// CreateComponentVersionInput is the configuration required for a new version.
type CreateComponentVersionInput struct {
	Description      string          `json:"componentVersionDescription"`
	Dependencies     []Dependency    `json:"componentVersionDependencies"`
	ReleaseType      string          `json:"componentVersionReleaseType"`
	Definition       json.RawMessage `json:"componentVersionDefinition"`
	SoftwareVendor   string          `json:"softwareVendor"`
	SoftwareVersion  string          `json:"softwareVersion"`
	LicenseDashboard *string         `json:"licenseDashboard,omitempty"`
	Notes            *string         `json:"notes,omitempty"`
}

// UpdateComponentVersionInput contains every mutable component-version field.
type UpdateComponentVersionInput struct {
	Description      string          `json:"componentVersionDescription"`
	Dependencies     []Dependency    `json:"componentVersionDependencies"`
	Definition       json.RawMessage `json:"componentVersionDefinition"`
	SoftwareVendor   string          `json:"softwareVendor"`
	SoftwareVersion  string          `json:"softwareVersion"`
	LicenseDashboard *string         `json:"licenseDashboard,omitempty"`
	Notes            *string         `json:"notes,omitempty"`
}

// ComponentVersion is VEW's component-version representation.
type ComponentVersion struct {
	RetryAfter       time.Duration   `json:"-"`
	ComponentID      string          `json:"componentId"`
	ID               string          `json:"componentVersionId"`
	Description      string          `json:"componentVersionDescription"`
	Name             string          `json:"componentVersionName"`
	Dependencies     []Dependency    `json:"componentVersionDependencies"`
	Definition       json.RawMessage `json:"-"`
	SoftwareVendor   string          `json:"softwareVendor"`
	SoftwareVersion  string          `json:"softwareVersion"`
	LicenseDashboard *string         `json:"licenseDashboard"`
	Notes            *string         `json:"notes"`
	Status           string          `json:"status"`
	CreatedAt        string          `json:"createDate"`
	CreatedBy        string          `json:"createdBy"`
	UpdatedAt        string          `json:"lastUpdateDate"`
	UpdatedBy        string          `json:"lastUpdatedBy"`
}

// ComponentVersionAPI is the component-version API surface used by the Terraform resource.
type ComponentVersionAPI interface {
	CreateComponentVersion(context.Context, string, string, CreateComponentVersionInput) (ActionResult, error)
	GetComponentVersion(context.Context, string, string, string) (ComponentVersion, error)
	UpdateComponentVersion(context.Context, string, string, string, UpdateComponentVersionInput) (ActionResult, error)
	RetireComponentVersion(context.Context, string, string, string) (ActionResult, error)
}

var _ ComponentVersionAPI = (*Client)(nil)

// ComponentVersionReleaseAPI is the narrow component-version release surface
// used by callers configured with release-only credentials.
type ComponentVersionReleaseAPI interface {
	ReleaseComponentVersion(context.Context, string, string, string) error
}

var _ ComponentVersionReleaseAPI = (*Client)(nil)

// CreateComponentVersion creates a component version and returns its action details.
func (c *Client) CreateComponentVersion(ctx context.Context, projectID, componentID string, input CreateComponentVersionInput) (ActionResult, error) {
	body, err := json.Marshal(input)
	if err != nil {
		return ActionResult{}, errors.New("VEW component version create request could not be encoded")
	}
	segments, err := componentVersionSegments(projectID, componentID, "", false)
	if err != nil {
		return ActionResult{}, err
	}
	idempotencyKey := c.idempotencyKey()
	if idempotencyKey == "" {
		return ActionResult{}, errors.New("VEW component version idempotency key could not be generated")
	}
	response, headers, err := c.transport.Do(ctx, http.MethodPost, segments, body, idempotencyKey)
	if err != nil {
		return ActionResult{}, err
	}
	return decodeComponentVersionAction(response, headers, "create")
}

// GetComponentVersion fetches one component version.
func (c *Client) GetComponentVersion(ctx context.Context, projectID, componentID, versionID string) (ComponentVersion, error) {
	segments, err := componentVersionSegments(projectID, componentID, versionID, true)
	if err != nil {
		return ComponentVersion{}, err
	}
	response, headers, err := c.transport.Do(ctx, http.MethodGet, segments, nil, "")
	if err != nil {
		return ComponentVersion{}, err
	}
	var envelope struct {
		ComponentVersion ComponentVersion `json:"component_version"`
		Definition       json.RawMessage  `json:"componentVersionDefinition"`
	}
	if err := json.Unmarshal(response, &envelope); err != nil {
		return ComponentVersion{}, errors.New("VEW component version response could not be decoded")
	}
	if envelope.ComponentVersion.ID == "" {
		return ComponentVersion{}, errors.New("VEW component version response missing component version")
	}
	envelope.ComponentVersion.Definition = envelope.Definition
	envelope.ComponentVersion.RetryAfter = vew.RetryAfter(headers, time.Now())
	return envelope.ComponentVersion, nil
}

// UpdateComponentVersion updates a component version and returns its action details.
func (c *Client) UpdateComponentVersion(ctx context.Context, projectID, componentID, versionID string, input UpdateComponentVersionInput) (ActionResult, error) {
	body, err := json.Marshal(input)
	if err != nil {
		return ActionResult{}, errors.New("VEW component version update request could not be encoded")
	}
	segments, err := componentVersionSegments(projectID, componentID, versionID, true)
	if err != nil {
		return ActionResult{}, err
	}
	response, headers, err := c.transport.Do(ctx, http.MethodPut, segments, body, "")
	if err != nil {
		return ActionResult{}, err
	}
	return decodeComponentVersionAction(response, headers, "update")
}

// RetireComponentVersion retires a component version and returns its action details.
func (c *Client) RetireComponentVersion(ctx context.Context, projectID, componentID, versionID string) (ActionResult, error) {
	segments, err := componentVersionSegments(projectID, componentID, versionID, true)
	if err != nil {
		return ActionResult{}, err
	}
	response, headers, err := c.transport.Do(ctx, http.MethodDelete, segments, nil, "")
	if err != nil {
		return ActionResult{}, err
	}
	return decodeComponentVersionAction(response, headers, "retire")
}

// ReleaseComponentVersion promotes an existing component version. The release
// endpoint is terminal and idempotent, so it intentionally uses neither a
// request body nor a create idempotency key.
func (c *Client) ReleaseComponentVersion(ctx context.Context, projectID, componentID, versionID string) error {
	segments, err := componentVersionSegments(projectID, componentID, versionID, true)
	if err != nil {
		return err
	}
	response, headers, err := c.transport.Do(ctx, http.MethodPost, append(segments, "release"), nil, "")
	if err != nil {
		return err
	}
	result, err := decodeComponentVersionAction(response, headers, "release")
	if err != nil {
		return err
	}
	if result.ID != versionID {
		return errors.New("VEW component version release response ID did not match requested version")
	}
	return nil
}

func componentVersionSegments(projectID, componentID, versionID string, item bool) ([]string, error) {
	segments, err := componentSegments(projectID, componentID, true)
	if err != nil {
		return nil, err
	}
	segments = append(segments, "versions")
	if item {
		if strings.TrimSpace(versionID) == "" {
			return nil, errors.New("VEW component version ID must not be empty")
		}
		segments = append(segments, versionID)
	}
	return segments, nil
}

func decodeComponentVersionAction(response []byte, headers http.Header, action string) (ActionResult, error) {
	var envelope struct {
		ID string `json:"componentVersionId"`
	}
	if err := json.Unmarshal(response, &envelope); err != nil {
		return ActionResult{}, errors.New("VEW component version " + action + " response could not be decoded")
	}
	if envelope.ID == "" {
		return ActionResult{}, errors.New("VEW component version " + action + " response missing component version ID")
	}
	return ActionResult{ID: envelope.ID, RetryAfter: vew.RetryAfter(headers, time.Now())}, nil
}
