package components

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	"uuid"
)

// API is the component API surface used by the Terraform resource.
type API interface {
	CreateComponent(context.Context, string, CreateComponentInput) (string, error)
	GetComponent(context.Context, string, string) (Component, error)
	UpdateComponent(context.Context, string, string, UpdateComponentInput) error
	ArchiveComponent(context.Context, string, string) error
}

// ComponentReadAPI is the least-privilege interface for component data sources.
type ComponentReadAPI interface {
	GetComponent(context.Context, string, string) (Component, error)
}

// Client is the VEW component-domain API client.
type Client struct {
	transport      *vew.Transport
	idempotencyKey func() string
}

var _ API = (*Client)(nil)
var _ ComponentReadAPI = (*Client)(nil)

// NewClient constructs a component client using a shared authenticated transport.
func NewClient(transport *vew.Transport) *Client {
	return &Client{transport: transport, idempotencyKey: func() string { return uuid.New().String() }}
}

func componentSegments(projectID, componentID string, item bool) ([]string, error) {
	if strings.TrimSpace(projectID) == "" {
		return nil, errors.New("VEW project ID must not be empty")
	}
	if item && strings.TrimSpace(componentID) == "" {
		return nil, errors.New("VEW component ID must not be empty")
	}
	segments := []string{"projects", projectID, "components"}
	if item {
		segments = append(segments, componentID)
	}
	return segments, nil
}

// CreateComponent creates a component and returns its VEW identifier.
func (c *Client) CreateComponent(ctx context.Context, projectID string, input CreateComponentInput) (string, error) {
	body, err := json.Marshal(input)
	if err != nil {
		return "", errors.New("VEW component create request could not be encoded")
	}
	segments, err := componentSegments(projectID, "", false)
	if err != nil {
		return "", err
	}
	idempotencyKey := c.idempotencyKey()
	if idempotencyKey == "" {
		return "", errors.New("VEW component idempotency key could not be generated")
	}
	response, _, err := c.transport.Do(ctx, http.MethodPost, segments, body, idempotencyKey)
	if err != nil {
		return "", err
	}
	var envelope struct {
		ID string `json:"componentId"`
	}
	if err := json.Unmarshal(response, &envelope); err != nil {
		return "", errors.New("VEW component create response could not be decoded")
	}
	if envelope.ID == "" {
		return "", errors.New("VEW component create response missing component ID")
	}
	return envelope.ID, nil
}

// GetComponent fetches one component.
func (c *Client) GetComponent(ctx context.Context, projectID, componentID string) (Component, error) {
	segments, err := componentSegments(projectID, componentID, true)
	if err != nil {
		return Component{}, err
	}
	response, _, err := c.transport.Do(ctx, http.MethodGet, segments, nil, "")
	if err != nil {
		return Component{}, err
	}
	var envelope struct {
		Component Component `json:"component"`
	}
	if err := json.Unmarshal(response, &envelope); err != nil {
		return Component{}, errors.New("VEW component response could not be decoded")
	}
	if envelope.Component.ID == "" {
		return Component{}, errors.New("VEW component response missing component")
	}
	return envelope.Component, nil
}

// UpdateComponent updates a component description.
func (c *Client) UpdateComponent(ctx context.Context, projectID, componentID string, input UpdateComponentInput) error {
	body, err := json.Marshal(input)
	if err != nil {
		return errors.New("VEW component update request could not be encoded")
	}
	segments, err := componentSegments(projectID, componentID, true)
	if err != nil {
		return err
	}
	_, _, err = c.transport.Do(ctx, http.MethodPut, segments, body, "")
	return err
}

// ArchiveComponent archives a component. A missing component is already archived.
func (c *Client) ArchiveComponent(ctx context.Context, projectID, componentID string) error {
	segments, err := componentSegments(projectID, componentID, true)
	if err != nil {
		return err
	}
	_, _, err = c.transport.Do(ctx, http.MethodDelete, segments, nil, "")
	if vew.IsNotFound(err) {
		return nil
	}
	return err
}
