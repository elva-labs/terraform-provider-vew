// Package baseimages releases images to a deployment's base-image channels
// on the VEW Packaging S2S API. A channel (for example test or prod) per
// architecture names the image every project builds on; VEW decides which
// project may release and which channel gates another (prod only takes an
// image that is or was in test).
package baseimages

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
)

// ReleaseInput releases one image of the releasing project to a channel.
type ReleaseInput struct {
	ProjectID string `json:"projectId"`
	ImageID   string `json:"imageId"`
}

// BaseImage is the current release of one channel and architecture.
type BaseImage struct {
	Architecture  string  `json:"architecture"`
	Channel       string  `json:"channel"`
	OSVersion     string  `json:"osVersion"`
	ParameterName string  `json:"parameterName"`
	Status        string  `json:"status"`
	ProjectID     *string `json:"projectId"`
	ImageID       *string `json:"imageId"`
	AmiID         *string `json:"amiId"`
	PreviousAmiID *string `json:"previousAmiId"`
}

// API is the release lifecycle used by the Terraform resource.
type API interface {
	GetBaseImage(context.Context, string, string) (BaseImage, error)
	ReleaseBaseImage(context.Context, string, string, ReleaseInput) (BaseImage, error)
}

// Client keeps base_image read and write scopes on separate transports.
type Client struct {
	write *vew.Transport
	read  *vew.Transport
}

var _ API = (*Client)(nil)

// NewClient constructs a client from base_image.write and base_image.read transports.
func NewClient(write, read *vew.Transport) *Client {
	return &Client{write: write, read: read}
}

func segments(architecture, channel string) ([]string, error) {
	if strings.TrimSpace(architecture) == "" || strings.TrimSpace(channel) == "" {
		return nil, errors.New("VEW base image architecture and channel must not be empty")
	}
	return []string{"base-images", architecture, channel}, nil
}

func decode(body []byte) (BaseImage, error) {
	var image BaseImage
	if json.Unmarshal(body, &image) != nil || image.Channel == "" {
		return BaseImage{}, errors.New("VEW base image response could not be decoded")
	}
	return image, nil
}

// GetBaseImage reads a channel; vew.IsNotFound when the deployment has no such channel.
func (c *Client) GetBaseImage(ctx context.Context, architecture, channel string) (BaseImage, error) {
	path, err := segments(architecture, channel)
	if err != nil {
		return BaseImage{}, err
	}
	body, _, err := c.read.Do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return BaseImage{}, err
	}
	return decode(body)
}

// ReleaseBaseImage points a channel at an image. Repeating it changes nothing.
func (c *Client) ReleaseBaseImage(ctx context.Context, architecture, channel string, input ReleaseInput) (BaseImage, error) {
	path, err := segments(architecture, channel)
	if err != nil {
		return BaseImage{}, err
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return BaseImage{}, errors.New("VEW base image release request could not be encoded")
	}
	body, _, err := c.write.Do(ctx, http.MethodPut, path, payload, "")
	if err != nil {
		return BaseImage{}, err
	}
	return decode(body)
}
