package recipes

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	"uuid"
)

// RecipeAPI is the recipe surface required by the Terraform resource.
type RecipeAPI interface {
	CreateRecipe(context.Context, string, CreateRecipeInput) (string, error)
	GetRecipe(context.Context, string, string) (Recipe, error)
	ArchiveRecipe(context.Context, string, string) error
}

// Client accesses the recipe domain through VEW's authenticated transport.
type Client struct {
	transport      *vew.Transport
	idempotencyKey func() string
}

var _ RecipeAPI = (*Client)(nil)

func NewClient(transport *vew.Transport) *Client {
	return &Client{transport: transport, idempotencyKey: func() string { return uuid.New().String() }}
}

func recipeSegments(projectID, recipeID string, item bool) ([]string, error) {
	if strings.TrimSpace(projectID) == "" {
		return nil, errors.New("VEW project ID must not be empty")
	}
	segments := []string{"projects", projectID, "recipes"}
	if item {
		if strings.TrimSpace(recipeID) == "" {
			return nil, errors.New("VEW recipe ID must not be empty")
		}
		segments = append(segments, recipeID)
	}
	return segments, nil
}

func (c *Client) CreateRecipe(ctx context.Context, projectID string, input CreateRecipeInput) (string, error) {
	segments, err := recipeSegments(projectID, "", false)
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(input)
	if err != nil {
		return "", errors.New("VEW recipe create request could not be encoded")
	}
	key := c.idempotencyKey()
	if key == "" {
		return "", errors.New("VEW recipe idempotency key could not be generated")
	}
	response, _, err := c.transport.Do(ctx, http.MethodPost, segments, body, key)
	if err != nil {
		return "", err
	}
	var envelope struct {
		ID string `json:"recipeId"`
	}
	if json.Unmarshal(response, &envelope) != nil || envelope.ID == "" {
		return "", errors.New("VEW recipe create response missing recipe ID")
	}
	return envelope.ID, nil
}

func (c *Client) GetRecipe(ctx context.Context, projectID, recipeID string) (Recipe, error) {
	segments, err := recipeSegments(projectID, recipeID, true)
	if err != nil {
		return Recipe{}, err
	}
	response, _, err := c.transport.Do(ctx, http.MethodGet, segments, nil, "")
	if err != nil {
		return Recipe{}, err
	}
	var envelope struct {
		Recipe Recipe `json:"recipe"`
	}
	if json.Unmarshal(response, &envelope) != nil || envelope.Recipe.ID == "" {
		return Recipe{}, errors.New("VEW recipe response missing recipe")
	}
	return envelope.Recipe, nil
}

func (c *Client) ArchiveRecipe(ctx context.Context, projectID, recipeID string) error {
	segments, err := recipeSegments(projectID, recipeID, true)
	if err != nil {
		return err
	}
	_, _, err = c.transport.Do(ctx, http.MethodDelete, segments, nil, "")
	if vew.IsNotFound(err) {
		return nil
	}
	return err
}
