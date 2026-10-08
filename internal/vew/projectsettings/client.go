// Package projectsettings manages project-level settings on the VEW Projects
// S2S API: the external management mode and the workbench lifecycle policy.
// Both are singletons per project: PUT upserts, GET is 404 while unset, and
// DELETE is idempotent.
package projectsettings

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
)

// ManagementInput marks a project as managed by an external tool. VEW then
// refuses configuration changes made by signed-in users and points them to
// Source instead.
type ManagementInput struct {
	ManagedBy string `json:"managedBy"`
	Source    string `json:"source"`
}

// Management is the stored management mode.
type Management struct {
	ProjectID string `json:"projectId"`
	ManagedBy string `json:"managedBy"`
	Source    string `json:"source"`
}

// WorkbenchLifecycle is a project's workbench stop policy. Nil pointers are
// omitted and fall back to the deployment's defaults.
type WorkbenchLifecycle struct {
	AlwaysOn                    bool   `json:"alwaysOn"`
	IdleStopMinutes             *int64 `json:"idleStopMinutes,omitempty"`
	NightlyStop                 *bool  `json:"nightlyStop,omitempty"`
	WeekendStop                 *bool  `json:"weekendStop,omitempty"`
	AllowUserDisableNightlyStop bool   `json:"allowUserDisableNightlyStop"`
	AllowUserIdleTimeout        bool   `json:"allowUserIdleTimeout"`
	UserIdleTimeoutMinMinutes   int64  `json:"userIdleTimeoutMinMinutes"`
	UserIdleTimeoutMaxMinutes   int64  `json:"userIdleTimeoutMaxMinutes"`
}

// API is the settings lifecycle used by the Terraform resources.
type API interface {
	GetManagement(context.Context, string) (Management, error)
	PutManagement(context.Context, string, ManagementInput) error
	DeleteManagement(context.Context, string) error
	GetWorkbenchLifecycle(context.Context, string) (WorkbenchLifecycle, error)
	PutWorkbenchLifecycle(context.Context, string, WorkbenchLifecycle) error
	DeleteWorkbenchLifecycle(context.Context, string) error
}

// Client keeps program read and write scopes on separate transports.
type Client struct {
	write *vew.Transport
	read  *vew.Transport
}

var _ API = (*Client)(nil)

// NewClient constructs a settings client from program.write and program.read transports.
func NewClient(write, read *vew.Transport) *Client {
	return &Client{write: write, read: read}
}

func segments(projectID, setting string) ([]string, error) {
	if strings.TrimSpace(projectID) == "" {
		return nil, errors.New("VEW project ID must not be empty")
	}
	return []string{"projects", projectID, setting}, nil
}

func (c *Client) get(ctx context.Context, projectID, setting string, out any) error {
	path, err := segments(projectID, setting)
	if err != nil {
		return err
	}
	body, _, err := c.read.Do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return err
	}
	if json.Unmarshal(body, out) != nil {
		return errors.New("VEW project " + setting + " response could not be decoded")
	}
	return nil
}

func (c *Client) put(ctx context.Context, projectID, setting string, input any) error {
	path, err := segments(projectID, setting)
	if err != nil {
		return err
	}
	body, err := json.Marshal(input)
	if err != nil {
		return errors.New("VEW project " + setting + " request could not be encoded")
	}
	_, _, err = c.write.Do(ctx, http.MethodPut, path, body, "")
	return err
}

func (c *Client) delete(ctx context.Context, projectID, setting string) error {
	path, err := segments(projectID, setting)
	if err != nil {
		return err
	}
	_, _, err = c.write.Do(ctx, http.MethodDelete, path, nil, "")
	if vew.IsNotFound(err) {
		return nil
	}
	return err
}

// GetManagement returns the management mode; vew.IsNotFound while the project is not externally managed.
func (c *Client) GetManagement(ctx context.Context, projectID string) (Management, error) {
	var m Management
	err := c.get(ctx, projectID, "management", &m)
	return m, err
}

// PutManagement sets the management mode.
func (c *Client) PutManagement(ctx context.Context, projectID string, input ManagementInput) error {
	return c.put(ctx, projectID, "management", input)
}

// DeleteManagement hands the project back to the portal.
func (c *Client) DeleteManagement(ctx context.Context, projectID string) error {
	return c.delete(ctx, projectID, "management")
}

// GetWorkbenchLifecycle returns the policy; vew.IsNotFound while the project uses the defaults.
func (c *Client) GetWorkbenchLifecycle(ctx context.Context, projectID string) (WorkbenchLifecycle, error) {
	var l WorkbenchLifecycle
	err := c.get(ctx, projectID, "workbench-lifecycle", &l)
	return l, err
}

// PutWorkbenchLifecycle replaces the whole policy.
func (c *Client) PutWorkbenchLifecycle(ctx context.Context, projectID string, input WorkbenchLifecycle) error {
	return c.put(ctx, projectID, "workbench-lifecycle", input)
}

// DeleteWorkbenchLifecycle returns the project to the deployment's defaults.
func (c *Client) DeleteWorkbenchLifecycle(ctx context.Context, projectID string) error {
	return c.delete(ctx, projectID, "workbench-lifecycle")
}
