package recipes

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
)

// ComponentVersion identifies one caller-selected recipe component version.
type ComponentVersion struct {
	ComponentID   string `json:"componentId"`
	ComponentName string `json:"componentName"`
	VersionID     string `json:"componentVersionId"`
	VersionName   string `json:"componentVersionName"`
	Type          string `json:"componentVersionType"`
	Order         int64  `json:"order"`
}

// BaseImageChannel is omitted when empty: VEW then builds on prod (create) or keeps the
// version's channel (update), and recipes outside the base image entries never send one.
type CreateRecipeVersionInput struct {
	Components       []ComponentVersion `json:"configuredComponentsVersions"`
	Description      string             `json:"recipeVersionDescription"`
	ReleaseType      string             `json:"recipeVersionReleaseType"`
	VolumeSize       string             `json:"recipeVersionVolumeSize"`
	Integrations     []string           `json:"recipeVersionIntegrations"`
	BaseImageChannel string             `json:"baseImageChannel,omitempty"`
}

type UpdateRecipeVersionInput struct {
	Components       []ComponentVersion `json:"configuredComponentsVersions"`
	Description      string             `json:"recipeVersionDescription"`
	VolumeSize       string             `json:"recipeVersionVolumeSize"`
	Integrations     []string           `json:"recipeVersionIntegrations"`
	BaseImageChannel string             `json:"baseImageChannel,omitempty"`
}

// RecipeVersion preserves nil Components for historical versions whose
// configured selection was never stored; a pointer to [] is an explicit empty selection.
type RecipeVersion struct {
	RetryAfter          time.Duration       `json:"-"`
	RecipeID            string              `json:"recipeId"`
	ID                  string              `json:"recipeVersionId"`
	Components          *[]ComponentVersion `json:"configuredComponentsVersions"`
	EffectiveComponents []ComponentVersion  `json:"effectiveComponentsVersions"`
	Description         string              `json:"recipeVersionDescription"`
	Name                string              `json:"recipeVersionName"`
	VolumeSize          string              `json:"recipeVersionVolumeSize"`
	Integrations        []string            `json:"recipeVersionIntegrations"`
	BaseImageChannel    *string             `json:"baseImageChannel"`
	Status              string              `json:"status"`
	CreatedAt           string              `json:"createDate"`
	CreatedBy           string              `json:"createdBy"`
	UpdatedAt           string              `json:"lastUpdateDate"`
	UpdatedBy           string              `json:"lastUpdatedBy"`
}

type ActionResult struct {
	ID         string
	RetryAfter time.Duration
}

// RecipeVersionAPI is the version surface required by the Terraform resource.
type RecipeVersionAPI interface {
	CreateRecipeVersion(context.Context, string, string, CreateRecipeVersionInput) (ActionResult, error)
	GetRecipeVersion(context.Context, string, string, string) (RecipeVersion, error)
	UpdateRecipeVersion(context.Context, string, string, string, UpdateRecipeVersionInput) (ActionResult, error)
	RetireRecipeVersion(context.Context, string, string, string) (ActionResult, error)
}

// RecipeVersionReadAPI is the least-privilege surface used by recipe-version data sources.
type RecipeVersionReadAPI interface {
	GetRecipeVersion(context.Context, string, string, string) (RecipeVersion, error)
}

var _ RecipeVersionReadAPI = (*Client)(nil)

var _ RecipeVersionAPI = (*Client)(nil)

// RecipeVersionReleaseAPI is the narrow recipe-version release surface used
// by callers configured with release-only credentials.
type RecipeVersionReleaseAPI interface {
	ReleaseRecipeVersion(context.Context, string, string, string) error
}

var _ RecipeVersionReleaseAPI = (*Client)(nil)

func versionSegments(projectID, recipeID, versionID string, item bool) ([]string, error) {
	segments, err := recipeSegments(projectID, recipeID, true)
	if err != nil {
		return nil, err
	}
	segments = append(segments, "versions")
	if item {
		if strings.TrimSpace(versionID) == "" {
			return nil, errors.New("VEW recipe version ID must not be empty")
		}
		segments = append(segments, versionID)
	}
	return segments, nil
}

func (c *Client) CreateRecipeVersion(ctx context.Context, projectID, recipeID string, input CreateRecipeVersionInput) (ActionResult, error) {
	segments, err := versionSegments(projectID, recipeID, "", false)
	if err != nil {
		return ActionResult{}, err
	}
	body, err := json.Marshal(input)
	if err != nil {
		return ActionResult{}, errors.New("VEW recipe version create request could not be encoded")
	}
	key := c.idempotencyKey()
	if key == "" {
		return ActionResult{}, errors.New("VEW recipe version idempotency key could not be generated")
	}
	response, headers, err := c.transport.Do(ctx, http.MethodPost, segments, body, key)
	if err != nil {
		return ActionResult{}, err
	}
	return decodeVersionAction(response, headers, "create")
}

func (c *Client) GetRecipeVersion(ctx context.Context, projectID, recipeID, versionID string) (RecipeVersion, error) {
	segments, err := versionSegments(projectID, recipeID, versionID, true)
	if err != nil {
		return RecipeVersion{}, err
	}
	response, headers, err := c.transport.Do(ctx, http.MethodGet, segments, nil, "")
	if err != nil {
		return RecipeVersion{}, err
	}
	var envelope struct {
		Version RecipeVersion `json:"recipe_version"`
	}
	if json.Unmarshal(response, &envelope) != nil || envelope.Version.ID == "" {
		return RecipeVersion{}, errors.New("VEW recipe version response missing recipe version")
	}
	envelope.Version.RetryAfter = vew.RetryAfter(headers, time.Now())
	return envelope.Version, nil
}

func (c *Client) UpdateRecipeVersion(ctx context.Context, projectID, recipeID, versionID string, input UpdateRecipeVersionInput) (ActionResult, error) {
	segments, err := versionSegments(projectID, recipeID, versionID, true)
	if err != nil {
		return ActionResult{}, err
	}
	body, err := json.Marshal(input)
	if err != nil {
		return ActionResult{}, errors.New("VEW recipe version update request could not be encoded")
	}
	response, headers, err := c.transport.Do(ctx, http.MethodPut, segments, body, "")
	if err != nil {
		return ActionResult{}, err
	}
	return decodeVersionAction(response, headers, "update")
}

func (c *Client) RetireRecipeVersion(ctx context.Context, projectID, recipeID, versionID string) (ActionResult, error) {
	segments, err := versionSegments(projectID, recipeID, versionID, true)
	if err != nil {
		return ActionResult{}, err
	}
	response, headers, err := c.transport.Do(ctx, http.MethodDelete, segments, nil, "")
	if err != nil {
		return ActionResult{}, err
	}
	return decodeVersionAction(response, headers, "retire")
}

// ReleaseRecipeVersion promotes an existing recipe version. The release
// endpoint is terminal and idempotent, so it intentionally uses neither a
// request body nor a create idempotency key.
func (c *Client) ReleaseRecipeVersion(ctx context.Context, projectID, recipeID, versionID string) error {
	segments, err := versionSegments(projectID, recipeID, versionID, true)
	if err != nil {
		return err
	}
	response, headers, err := c.transport.Do(ctx, http.MethodPost, append(segments, "release"), nil, "")
	if err != nil {
		return err
	}
	result, err := decodeVersionAction(response, headers, "release")
	if err != nil {
		return err
	}
	if result.ID != versionID {
		return errors.New("VEW recipe version release response ID did not match requested version")
	}
	return nil
}

func decodeVersionAction(response []byte, headers http.Header, action string) (ActionResult, error) {
	var envelope struct {
		ID string `json:"recipeVersionId"`
	}
	if json.Unmarshal(response, &envelope) != nil || envelope.ID == "" {
		return ActionResult{}, errors.New("VEW recipe version " + action + " response missing recipe version ID")
	}
	return ActionResult{ID: envelope.ID, RetryAfter: vew.RetryAfter(headers, time.Now())}, nil
}
