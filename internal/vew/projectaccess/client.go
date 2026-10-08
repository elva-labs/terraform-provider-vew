package projectaccess

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
)

// ProjectInput is the desired project. An unset Description is omitted rather than sent as null:
// VEW treats a missing description as none, while API Gateway's request validator (JSON Schema
// draft 4) may reject null for a field typed as string even when the OpenAPI schema declares it
// nullable.
type ProjectInput struct {
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
	IsActive    bool    `json:"isActive"`
	// Omitted keeps the project's current value; a new project allows remote support.
	RemoteSupportEnabled *bool `json:"remoteSupportEnabled,omitempty"`
	// "full" or "workbench-only"; omitted keeps the project's current value (a new project is "full").
	Experience *string `json:"experience,omitempty"`
}
type Project struct {
	ID          string  `json:"projectId"`
	Name        string  `json:"projectName"`
	Description *string `json:"projectDescription"`
	IsActive    bool    `json:"isActive"`
	// Whether the project's support staff may ask to join its users' desktops; nil from older APIs.
	RemoteSupportEnabled *bool `json:"remoteSupportEnabled"`
	// What the project's members get in the portal; nil from older APIs.
	Experience *string `json:"experience"`
	CreatedAt  string  `json:"createDate"`
	UpdatedAt  string  `json:"lastUpdateDate"`
}
type UserInput struct {
	Roles       []string `json:"roles"`
	Email       *string  `json:"userEmail,omitempty"`
	DisplayName *string  `json:"userDisplayName,omitempty"`
}
type UserAssignment struct {
	ProjectID   string   `json:"projectId"`
	UserID      string   `json:"userId"`
	Roles       []string `json:"roles"`
	Email       *string  `json:"userEmail"`
	DisplayName *string  `json:"userDisplayName"`
}
type GroupInput struct {
	Roles     []string `json:"roles"`
	GroupName *string  `json:"groupName,omitempty"`
}
type GroupAssignment struct {
	ProjectID string   `json:"projectId"`
	GroupID   string   `json:"groupId"`
	Roles     []string `json:"roles"`
	GroupName *string  `json:"groupName"`
}
type ClientAssignment struct {
	ProjectID string `json:"projectId"`
	ClientID  string `json:"clientId"`
	Status    string `json:"status"`
}

type API interface {
	CreateProject(context.Context, ProjectInput, string) (string, error)
	GetProject(context.Context, string) (Project, error)
	UpdateProject(context.Context, string, ProjectInput) error
	DeactivateProject(context.Context, string) error
	CreateUser(context.Context, string, string, UserInput) error
	GetUser(context.Context, string, string) (UserAssignment, error)
	UpdateUser(context.Context, string, string, UserInput) error
	DeleteUser(context.Context, string, string) error
	PutGroup(context.Context, string, string, GroupInput) error
	GetGroup(context.Context, string, string) (GroupAssignment, error)
	DeleteGroup(context.Context, string, string) error
	ActivateClient(context.Context, string, string) error
	GetClient(context.Context, string, string) (ClientAssignment, error)
	RevokeClient(context.Context, string, string) error
}

type Pair struct{ Write, Read *vew.Transport }
type Client struct{ Program, User, Group, Service Pair }

var _ API = (*Client)(nil)

func NewClient(program, user, group, service Pair) *Client {
	return &Client{program, user, group, service}
}
func segments(projectID, kind, id string) ([]string, error) {
	if strings.TrimSpace(projectID) == "" {
		return nil, errors.New("missing VEW project ID")
	}
	out := []string{"projects", projectID}
	if kind != "" {
		out = append(out, kind)
	}
	if id != "" {
		out = append(out, id)
	}
	return out, nil
}
func encode(v any) ([]byte, error) {
	b, e := json.Marshal(v)
	if e != nil {
		return nil, errors.New("VEW request could not be encoded")
	}
	return b, nil
}
func call(ctx context.Context, t *vew.Transport, method string, path []string, body any, key string) ([]byte, error) {
	var payload []byte
	if body != nil {
		var e error
		payload, e = encode(body)
		if e != nil {
			return nil, e
		}
	}
	response, _, e := t.Do(ctx, method, path, payload, key)
	return response, e
}
func decode[T any](body []byte) (T, error) {
	var result T
	if json.Unmarshal(body, &result) != nil {
		return result, errors.New("VEW response could not be decoded")
	}
	return result, nil
}
func (c *Client) CreateProject(ctx context.Context, input ProjectInput, key string) (string, error) {
	b, e := call(ctx, c.Program.Write, http.MethodPost, []string{"projects"}, input, key)
	if e != nil {
		return "", e
	}
	v, e := decode[struct {
		ID string `json:"projectId"`
	}](b)
	if e != nil {
		return "", e
	}
	if v.ID == "" {
		return "", errors.New("VEW project create response missing ID")
	}
	return v.ID, nil
}
func (c *Client) GetProject(ctx context.Context, id string) (Project, error) {
	p, e := segments(id, "", "")
	if e != nil {
		return Project{}, e
	}
	b, e := call(ctx, c.Program.Read, http.MethodGet, p, nil, "")
	if e != nil {
		return Project{}, e
	}
	v, e := decode[Project](b)
	if e != nil {
		return v, e
	}
	if v.ID == "" {
		return v, errors.New("VEW project response missing ID")
	}
	return v, nil
}
func (c *Client) UpdateProject(ctx context.Context, id string, input ProjectInput) error {
	p, e := segments(id, "", "")
	if e != nil {
		return e
	}
	_, e = call(ctx, c.Program.Write, http.MethodPut, p, input, "")
	return e
}
func (c *Client) DeactivateProject(ctx context.Context, id string) error {
	p, e := segments(id, "", "")
	if e != nil {
		return e
	}
	_, e = call(ctx, c.Program.Write, http.MethodDelete, p, nil, "")
	if vew.IsNotFound(e) {
		return nil
	}
	return e
}
func (c *Client) CreateUser(ctx context.Context, pid, uid string, input UserInput) error {
	p, e := segments(pid, "users", "")
	if e != nil {
		return e
	}
	body := struct {
		UserID      string   `json:"userId"`
		Roles       []string `json:"roles"`
		Email       *string  `json:"userEmail,omitempty"`
		DisplayName *string  `json:"userDisplayName,omitempty"`
	}{uid, input.Roles, input.Email, input.DisplayName}
	_, e = call(ctx, c.User.Write, http.MethodPost, p, body, "")
	return e
}
func (c *Client) GetUser(ctx context.Context, pid, uid string) (UserAssignment, error) {
	p, e := segments(pid, "users", uid)
	if e != nil {
		return UserAssignment{}, e
	}
	b, e := call(ctx, c.User.Read, http.MethodGet, p, nil, "")
	if e != nil {
		return UserAssignment{}, e
	}
	return decode[UserAssignment](b)
}
func (c *Client) UpdateUser(ctx context.Context, pid, uid string, input UserInput) error {
	p, e := segments(pid, "users", uid)
	if e != nil {
		return e
	}
	_, e = call(ctx, c.User.Write, http.MethodPut, p, input, "")
	return e
}
func (c *Client) DeleteUser(ctx context.Context, pid, uid string) error {
	p, e := segments(pid, "users", uid)
	if e != nil {
		return e
	}
	_, e = call(ctx, c.User.Write, http.MethodDelete, p, nil, "")
	if vew.IsNotFound(e) {
		return nil
	}
	return e
}
func (c *Client) PutGroup(ctx context.Context, pid, gid string, input GroupInput) error {
	p, e := segments(pid, "groups", gid)
	if e != nil {
		return e
	}
	_, e = call(ctx, c.Group.Write, http.MethodPut, p, input, "")
	return e
}
func (c *Client) GetGroup(ctx context.Context, pid, gid string) (GroupAssignment, error) {
	p, e := segments(pid, "groups", gid)
	if e != nil {
		return GroupAssignment{}, e
	}
	b, e := call(ctx, c.Group.Read, http.MethodGet, p, nil, "")
	if e != nil {
		return GroupAssignment{}, e
	}
	return decode[GroupAssignment](b)
}
func (c *Client) DeleteGroup(ctx context.Context, pid, gid string) error {
	p, e := segments(pid, "groups", gid)
	if e != nil {
		return e
	}
	_, e = call(ctx, c.Group.Write, http.MethodDelete, p, nil, "")
	if vew.IsNotFound(e) {
		return nil
	}
	return e
}
func (c *Client) ActivateClient(ctx context.Context, pid, cid string) error {
	p, e := segments(pid, "clients", cid)
	if e != nil {
		return e
	}
	_, e = call(ctx, c.Service.Write, http.MethodPut, p, nil, "")
	return e
}
func (c *Client) GetClient(ctx context.Context, pid, cid string) (ClientAssignment, error) {
	p, e := segments(pid, "clients", cid)
	if e != nil {
		return ClientAssignment{}, e
	}
	b, e := call(ctx, c.Service.Read, http.MethodGet, p, nil, "")
	if e != nil {
		return ClientAssignment{}, e
	}
	var envelope struct {
		Assignment *ClientAssignment `json:"assignment"`
	}
	if json.Unmarshal(b, &envelope) != nil {
		return ClientAssignment{}, errors.New("VEW client assignment response could not be decoded")
	}
	if envelope.Assignment == nil {
		return ClientAssignment{}, errors.New("VEW client assignment response missing assignment")
	}
	return *envelope.Assignment, nil
}
func (c *Client) RevokeClient(ctx context.Context, pid, cid string) error {
	p, e := segments(pid, "clients", cid)
	if e != nil {
		return e
	}
	_, e = call(ctx, c.Service.Write, http.MethodDelete, p, nil, "")
	if vew.IsNotFound(e) {
		return nil
	}
	return e
}
