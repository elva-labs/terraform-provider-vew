// Package mandatorycomponents manages the mandatory components lists of the
// VEW Packaging S2S API. A list belongs to a platform, OS version and
// architecture; VEW adds its released component versions to every recipe
// version created on that OS - prepended before the recipe's own components,
// appended after them. The lists are global, so VEW only lets the releasing
// project (when the deployment has one) change them.
package mandatorycomponents

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
)

// ComponentRef names a released component version in a list.
type ComponentRef struct {
	ComponentID        string `json:"componentId"`
	ComponentVersionID string `json:"componentVersionId"`
}

// Component is a list entry as VEW returns it, names resolved.
type Component struct {
	ComponentID          string `json:"componentId"`
	ComponentName        string `json:"componentName"`
	ComponentVersionID   string `json:"componentVersionId"`
	ComponentVersionName string `json:"componentVersionName"`
	Order                *int64 `json:"order"`
}

// List is one mandatory components list.
type List struct {
	Platform       string      `json:"platform"`
	OSVersion      string      `json:"osVersion"`
	Architecture   string      `json:"architecture"`
	Prepended      []Component `json:"prependedComponentsVersions"`
	Appended       []Component `json:"appendedComponentsVersions"`
	LastUpdateDate *string     `json:"lastUpdateDate"`
	LastUpdatedBy  *string     `json:"lastUpdatedBy"`
}

// PutInput creates or replaces a list.
type PutInput struct {
	ProjectID string         `json:"projectId"`
	Prepended []ComponentRef `json:"prependedComponentsVersions"`
	Appended  []ComponentRef `json:"appendedComponentsVersions"`
}

// Key identifies a list.
type Key struct {
	Platform     string
	OSVersion    string
	Architecture string
}

// API is the list lifecycle used by the Terraform resource.
type API interface {
	GetList(context.Context, Key) (List, error)
	PutList(context.Context, Key, PutInput) (List, error)
	DeleteList(context.Context, Key, string) error
}

// Client keeps the read and write scopes on separate transports.
type Client struct {
	write *vew.Transport
	read  *vew.Transport
}

var _ API = (*Client)(nil)

// NewClient constructs a client from mandatory_components_list.write and .read transports.
func NewClient(write, read *vew.Transport) *Client {
	return &Client{write: write, read: read}
}

func segments(key Key) ([]string, error) {
	if strings.TrimSpace(key.Platform) == "" || strings.TrimSpace(key.OSVersion) == "" || strings.TrimSpace(key.Architecture) == "" {
		return nil, errors.New("VEW mandatory components list platform, OS version and architecture must not be empty")
	}
	return []string{"mandatory-components-lists", key.Platform, key.OSVersion, key.Architecture}, nil
}

func decode(body []byte) (List, error) {
	var list List
	if json.Unmarshal(body, &list) != nil || list.OSVersion == "" {
		return List{}, errors.New("VEW mandatory components list response could not be decoded")
	}
	return list, nil
}

// GetList reads a list; vew.IsNotFound when there is none.
func (c *Client) GetList(ctx context.Context, key Key) (List, error) {
	path, err := segments(key)
	if err != nil {
		return List{}, err
	}
	body, _, err := c.read.Do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return List{}, err
	}
	return decode(body)
}

// PutList creates or replaces a list. Repeating it changes nothing.
func (c *Client) PutList(ctx context.Context, key Key, input PutInput) (List, error) {
	path, err := segments(key)
	if err != nil {
		return List{}, err
	}
	if input.Prepended == nil {
		input.Prepended = []ComponentRef{}
	}
	if input.Appended == nil {
		input.Appended = []ComponentRef{}
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return List{}, errors.New("VEW mandatory components list request could not be encoded")
	}
	body, _, err := c.write.Do(ctx, http.MethodPut, path, payload, "")
	if err != nil {
		return List{}, err
	}
	return decode(body)
}

// DeleteList removes a list; removing a list that is gone is not an error.
func (c *Client) DeleteList(ctx context.Context, key Key, projectID string) error {
	path, err := segments(key)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]string{"projectId": projectID})
	if err != nil {
		return errors.New("VEW mandatory components list request could not be encoded")
	}
	_, _, err = c.write.Do(ctx, http.MethodDelete, path, payload, "")
	if vew.IsNotFound(err) {
		return nil
	}
	return err
}
