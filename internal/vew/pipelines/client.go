package pipelines

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	"uuid"
)

// API is the pipeline surface used by the Terraform resource.
type API interface {
	CreatePipeline(context.Context, string, CreatePipelineInput) (ActionResult, error)
	ListPipelines(context.Context, string) ([]Pipeline, error)
	GetPipeline(context.Context, string, string) (Pipeline, error)
	UpdatePipeline(context.Context, string, string, UpdatePipelineInput) (ActionResult, error)
	RetirePipeline(context.Context, string, string) (ActionResult, error)
}

// Client accesses project-scoped pipelines through VEW's authenticated transport.
type Client struct {
	transport      *vew.Transport
	idempotencyKey func() string
}

var _ API = (*Client)(nil)

func NewClient(transport *vew.Transport) *Client {
	return &Client{transport: transport, idempotencyKey: func() string { return uuid.New().String() }}
}

func pipelineSegments(projectID, pipelineID string, item bool) ([]string, error) {
	if strings.TrimSpace(projectID) == "" {
		return nil, errors.New("VEW project ID must not be empty")
	}
	segments := []string{"projects", projectID, "pipelines"}
	if item {
		if strings.TrimSpace(pipelineID) == "" {
			return nil, errors.New("VEW pipeline ID must not be empty")
		}
		segments = append(segments, pipelineID)
	}
	return segments, nil
}

func (c *Client) CreatePipeline(ctx context.Context, projectID string, input CreatePipelineInput) (ActionResult, error) {
	segments, err := pipelineSegments(projectID, "", false)
	if err != nil {
		return ActionResult{}, err
	}
	body, err := json.Marshal(input)
	if err != nil {
		return ActionResult{}, errors.New("VEW pipeline create request could not be encoded")
	}
	key := c.idempotencyKey()
	if key == "" {
		return ActionResult{}, errors.New("VEW pipeline idempotency key could not be generated")
	}
	response, headers, err := c.transport.Do(ctx, http.MethodPost, segments, body, key)
	if err != nil {
		return ActionResult{}, err
	}
	return decodeAction(response, headers, "create")
}

func (c *Client) ListPipelines(ctx context.Context, projectID string) ([]Pipeline, error) {
	segments, err := pipelineSegments(projectID, "", false)
	if err != nil {
		return nil, err
	}
	response, _, err := c.transport.Do(ctx, http.MethodGet, segments, nil, "")
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Pipelines []Pipeline `json:"pipelines"`
	}
	if json.Unmarshal(response, &envelope) != nil || envelope.Pipelines == nil {
		return nil, errors.New("VEW pipeline list response missing pipelines")
	}
	return envelope.Pipelines, nil
}

func (c *Client) GetPipeline(ctx context.Context, projectID, pipelineID string) (Pipeline, error) {
	segments, err := pipelineSegments(projectID, pipelineID, true)
	if err != nil {
		return Pipeline{}, err
	}
	response, headers, err := c.transport.Do(ctx, http.MethodGet, segments, nil, "")
	if err != nil {
		return Pipeline{}, err
	}
	var envelope struct {
		Pipeline Pipeline `json:"pipeline"`
	}
	if json.Unmarshal(response, &envelope) != nil || envelope.Pipeline.ID == "" {
		return Pipeline{}, errors.New("VEW pipeline response missing pipeline")
	}
	envelope.Pipeline.RetryAfter = vew.RetryAfter(headers, time.Now())
	return envelope.Pipeline, nil
}

func (c *Client) UpdatePipeline(ctx context.Context, projectID, pipelineID string, input UpdatePipelineInput) (ActionResult, error) {
	segments, err := pipelineSegments(projectID, pipelineID, true)
	if err != nil {
		return ActionResult{}, err
	}
	body, err := json.Marshal(input)
	if err != nil {
		return ActionResult{}, errors.New("VEW pipeline update request could not be encoded")
	}
	response, headers, err := c.transport.Do(ctx, http.MethodPut, segments, body, "")
	if err != nil {
		return ActionResult{}, err
	}
	return decodeAction(response, headers, "update")
}

func (c *Client) RetirePipeline(ctx context.Context, projectID, pipelineID string) (ActionResult, error) {
	segments, err := pipelineSegments(projectID, pipelineID, true)
	if err != nil {
		return ActionResult{}, err
	}
	response, headers, err := c.transport.Do(ctx, http.MethodDelete, segments, nil, "")
	if err != nil {
		return ActionResult{}, err
	}
	return decodeAction(response, headers, "retire")
}

func decodeAction(response []byte, headers http.Header, action string) (ActionResult, error) {
	var envelope struct {
		ID string `json:"pipelineId"`
	}
	if json.Unmarshal(response, &envelope) != nil || envelope.ID == "" {
		return ActionResult{}, errors.New("VEW pipeline " + action + " response missing pipeline ID")
	}
	return ActionResult{ID: envelope.ID, RetryAfter: vew.RetryAfter(headers, time.Now())}, nil
}
