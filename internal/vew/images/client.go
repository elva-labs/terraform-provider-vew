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

// ReadAPI is the least-privilege image surface used by data sources.
type ReadAPI interface {
	GetImage(context.Context, string, string) (Image, error)
	ListImages(context.Context, string) ([]Image, error)
}

// Client reads image state through a pipeline-read-scoped transport.
type Client struct {
	read *vew.Transport
}

var _ ReadAPI = (*Client)(nil)

func NewClient(read *vew.Transport) *Client {
	return &Client{read: read}
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
