// Package workbenchsizes manages per-user workbench size grants on the VEW
// Provisioning S2S API: which sizes and disks of the deployment's catalog a
// user may use in a project beyond everyone's. PUT upserts the set (the same
// set changes nothing), DELETE is idempotent, and GET lists the project's
// grants with the catalog.
package workbenchsizes

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
)

// Option is one entry of the deployment's size catalog.
type Option struct {
	Key           string `json:"key"`
	Kind          string `json:"kind"`
	Label         string `json:"label"`
	Family        string `json:"family,omitempty"`
	InstanceType  string `json:"instanceType,omitempty"`
	SizeGb        int64  `json:"sizeGb,omitempty"`
	AlwaysAllowed bool   `json:"alwaysAllowed"`
}

// Grant is a user's stored grant in a project.
type Grant struct {
	UserID    string   `json:"userId"`
	UserEmail string   `json:"userEmail,omitempty"`
	Sizes     []string `json:"sizes"`
	UpdatedBy string   `json:"updatedBy"`
	UpdatedAt string   `json:"updatedAt"`
}

// Grants is the project's catalog and every grant.
type Grants struct {
	Catalog []Option `json:"catalog"`
	Grants  []Grant  `json:"grants"`
}

type putInput struct {
	Sizes     []string `json:"sizes"`
	UserEmail string   `json:"userEmail,omitempty"`
}

type putOutput struct {
	Grant Grant `json:"grant"`
}

// API is the grant lifecycle used by the Terraform resource.
type API interface {
	List(ctx context.Context, projectID string) (Grants, error)
	Get(ctx context.Context, projectID, userID string) (Grant, error)
	Put(ctx context.Context, projectID, userID string, sizes []string, userEmail string) (Grant, error)
	Delete(ctx context.Context, projectID, userID string) error
}

// Client keeps the read and write scopes on separate transports.
type Client struct {
	write *vew.Transport
	read  *vew.Transport
}

var _ API = (*Client)(nil)

// NewClient constructs a client from workbench_size.write and workbench_size.read transports.
func NewClient(write, read *vew.Transport) *Client {
	return &Client{write: write, read: read}
}

// ErrNotGranted is returned by Get when the user has no grant in the project.
var ErrNotGranted = errors.New("VEW workbench size grant not found")

func projectPath(projectID string, rest ...string) ([]string, error) {
	if strings.TrimSpace(projectID) == "" {
		return nil, errors.New("VEW project ID must not be empty")
	}
	return append([]string{"projects", projectID, "workbench-sizes"}, rest...), nil
}

// List returns the catalog and every grant of the project.
func (c *Client) List(ctx context.Context, projectID string) (Grants, error) {
	var out Grants
	path, err := projectPath(projectID)
	if err != nil {
		return out, err
	}
	body, _, err := c.read.Do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return out, err
	}
	if json.Unmarshal(body, &out) != nil {
		return out, errors.New("VEW workbench sizes response could not be decoded")
	}
	return out, nil
}

// Get returns one user's grant; ErrNotGranted when there is none.
func (c *Client) Get(ctx context.Context, projectID, userID string) (Grant, error) {
	all, err := c.List(ctx, projectID)
	if err != nil {
		return Grant{}, err
	}
	for _, g := range all.Grants {
		if g.UserID == userID {
			return g, nil
		}
	}
	return Grant{}, ErrNotGranted
}

// Put sets the user's sizes beyond everyone's.
func (c *Client) Put(ctx context.Context, projectID, userID string, sizes []string, userEmail string) (Grant, error) {
	if strings.TrimSpace(userID) == "" {
		return Grant{}, errors.New("VEW user ID must not be empty")
	}
	path, err := projectPath(projectID, "users", userID)
	if err != nil {
		return Grant{}, err
	}
	if sizes == nil {
		sizes = []string{}
	}
	body, err := json.Marshal(putInput{Sizes: sizes, UserEmail: userEmail})
	if err != nil {
		return Grant{}, errors.New("VEW workbench size grant request could not be encoded")
	}
	respBody, _, err := c.write.Do(ctx, http.MethodPut, path, body, "")
	if err != nil {
		return Grant{}, err
	}
	var out putOutput
	if json.Unmarshal(respBody, &out) != nil {
		return Grant{}, errors.New("VEW workbench size grant response could not be decoded")
	}
	return out.Grant, nil
}

// Delete returns the user to everyone's sizes; a missing grant is fine.
func (c *Client) Delete(ctx context.Context, projectID, userID string) error {
	path, err := projectPath(projectID, "users", userID)
	if err != nil {
		return err
	}
	_, _, err = c.write.Do(ctx, http.MethodDelete, path, nil, "")
	if vew.IsNotFound(err) {
		return nil
	}
	return err
}
