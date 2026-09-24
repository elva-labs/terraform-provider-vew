package pipelines

import "time"

// CreatePipelineInput is the configuration accepted when creating a pipeline.
type CreatePipelineInput struct {
	Name               string   `json:"pipelineName"`
	Description        string   `json:"pipelineDescription"`
	RecipeID           string   `json:"recipeId"`
	RecipeVersionID    string   `json:"recipeVersionId"`
	BuildInstanceTypes []string `json:"buildInstanceTypes"`
	Schedule           string   `json:"pipelineSchedule"`
	ProductID          *string  `json:"productId,omitempty"`
}

// UpdatePipelineInput sends the complete desired mutable configuration. A nil
// ProductID is intentionally encoded as JSON null to clear the association.
type UpdatePipelineInput struct {
	RecipeVersionID    string   `json:"recipeVersionId"`
	BuildInstanceTypes []string `json:"buildInstanceTypes"`
	Schedule           string   `json:"pipelineSchedule"`
	ProductID          *string  `json:"productId"`
}

// Pipeline is one project-scoped pipeline returned by VEW.
type Pipeline struct {
	RetryAfter              time.Duration `json:"-"`
	ProjectID               string        `json:"projectId"`
	ID                      string        `json:"pipelineId"`
	Name                    string        `json:"pipelineName"`
	Description             string        `json:"pipelineDescription"`
	RecipeID                string        `json:"recipeId"`
	RecipeName              string        `json:"recipeName"`
	RecipeVersionID         string        `json:"recipeVersionId"`
	RecipeVersionName       string        `json:"recipeVersionName"`
	BuildInstanceTypes      []string      `json:"buildInstanceTypes"`
	Schedule                string        `json:"pipelineSchedule"`
	ProductID               *string       `json:"productId"`
	Status                  string        `json:"status"`
	DistributionConfigARN   *string       `json:"distributionConfigArn"`
	InfrastructureConfigARN *string       `json:"infrastructureConfigArn"`
	PipelineARN             *string       `json:"pipelineArn"`
	CreatedAt               string        `json:"createDate"`
	CreatedBy               string        `json:"createdBy"`
	UpdatedAt               string        `json:"lastUpdateDate"`
	UpdatedBy               string        `json:"lastUpdatedBy"`
}

// ActionResult describes an accepted asynchronous pipeline action.
type ActionResult struct {
	ID         string
	RetryAfter time.Duration
}
