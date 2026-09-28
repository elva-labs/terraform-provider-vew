package images

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
)

// API is the image-build surface used by the Terraform action.
type API interface {
	BuildImage(context.Context, string, string, string) (ActionResult, error)
	GetImage(context.Context, string, string) (Image, error)
}

// ReadAPI is the least-privilege image surface used by data sources.
type ReadAPI interface {
	GetImage(context.Context, string, string) (Image, error)
	ListImages(context.Context, string) ([]Image, error)
}

// Client keeps execute and read OAuth transports separate.
type Client struct {
	execute *vew.Transport
	read    *vew.Transport
}

var _ API = (*Client)(nil)
var _ ReadAPI = (*Client)(nil)

func NewClient(execute, read *vew.Transport) *Client {
	return &Client{execute: execute, read: read}
}

func imageSegments(projectID, imageID string, item bool) ([]string, error) {
	if strings.TrimSpace(projectID) == "" {
		return nil, errors.New("VEW project ID must not be empty")
	}
	segments := []string{"projects", projectID, "images"}
	if item {
		if strings.TrimSpace(imageID) == "" {
			return nil, errors.New("VEW image ID must not be empty")
		}
		segments = append(segments, imageID)
	}
	return segments, nil
}

// BuildImage starts one pipeline build. The caller owns the idempotency key so
// the same key can be reused across action-level recovery attempts.
func (c *Client) BuildImage(ctx context.Context, projectID, pipelineID, idempotencyKey string) (ActionResult, error) {
	segments, err := imageSegments(projectID, "", false)
	if err != nil {
		return ActionResult{}, err
	}
	if strings.TrimSpace(pipelineID) == "" {
		return ActionResult{}, errors.New("VEW pipeline ID must not be empty")
	}
	if strings.TrimSpace(idempotencyKey) == "" {
		return ActionResult{}, errors.New("VEW image build idempotency key must not be empty")
	}
	body, err := json.Marshal(struct {
		PipelineID string `json:"pipelineId"`
	}{PipelineID: pipelineID})
	if err != nil {
		return ActionResult{}, errors.New("VEW image build request could not be encoded")
	}
	response, headers, err := c.execute.Do(ctx, http.MethodPost, segments, body, idempotencyKey)
	if err != nil {
		return ActionResult{}, err
	}
	var envelope struct {
		ID string `json:"imageId"`
	}
	if json.Unmarshal(response, &envelope) != nil || strings.TrimSpace(envelope.ID) == "" {
		return ActionResult{}, errors.New("VEW image build response missing image ID")
	}
	return ActionResult{ID: envelope.ID, RetryAfter: vew.RetryAfter(headers, time.Now())}, nil
}

// GetImage reads one image build through the read-scoped transport.
func (c *Client) GetImage(ctx context.Context, projectID, imageID string) (Image, error) {
	segments, err := imageSegments(projectID, imageID, true)
	if err != nil {
		return Image{}, err
	}
	response, headers, err := c.read.Do(ctx, http.MethodGet, segments, nil, "")
	if err != nil {
		return Image{}, err
	}
	var envelope struct {
		Image Image `json:"image"`
	}
	if json.Unmarshal(response, &envelope) != nil || strings.TrimSpace(envelope.Image.ID) == "" {
		return Image{}, errors.New("VEW image response missing image")
	}
	envelope.Image.RetryAfter = vew.RetryAfter(headers, time.Now())
	return envelope.Image, nil
}

// ListImages reads every image exposed by a project-scoped collection.
func (c *Client) ListImages(ctx context.Context, projectID string) ([]Image, error) {
	segments, err := imageSegments(projectID, "", false)
	if err != nil {
		return nil, err
	}
	response, _, err := c.read.Do(ctx, http.MethodGet, segments, nil, "")
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Images []Image `json:"images"`
	}
	if json.Unmarshal(response, &envelope) != nil || envelope.Images == nil {
		return nil, errors.New("VEW image list response missing images")
	}
	for _, image := range envelope.Images {
		if strings.TrimSpace(image.ID) == "" {
			return nil, errors.New("VEW image list response contains image missing ID")
		}
	}
	return envelope.Images, nil
}
