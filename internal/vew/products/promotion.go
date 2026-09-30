package products

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
)

// Stages a product version can be promoted to, in promotion order.
var Stages = []string{"DEV", "QA", "PROD"}

// VersionStage is a version's aggregated status in one stage.
type VersionStage struct {
	Stage  string `json:"stage"`
	Status string `json:"status"`
}

// ProductVersion is a product version with the stages it is released to.
type ProductVersion struct {
	ID     string         `json:"versionId"`
	Name   string         `json:"versionName"`
	Type   string         `json:"versionType"`
	Stages []VersionStage `json:"stages"`
}

// Distribution is a version's publication to one account and region.
type Distribution struct {
	AWSAccountID string `json:"awsAccountId"`
	Region       string `json:"region"`
	Status       string `json:"status"`
}

// Promotion is a version's release to one stage.
type Promotion struct {
	ProjectID     string         `json:"projectId"`
	ProductID     string         `json:"productId"`
	VersionID     string         `json:"versionId"`
	VersionName   string         `json:"versionName"`
	Stage         string         `json:"stage"`
	Status        string         `json:"status"`
	Distributions []Distribution `json:"distributions"`
}

// VersionReadAPI lists product versions and their stages.
type VersionReadAPI interface {
	ListProductVersions(context.Context, string, string) ([]ProductVersion, error)
}

// PromotionAPI manages a version's release to a stage.
type PromotionAPI interface {
	GetPromotion(context.Context, string, string, string, string) (Promotion, error)
	// PromoteVersion releases a version to a stage. Repeating it returns the
	// current state and never promotes twice.
	PromoteVersion(context.Context, string, string, string, string) (Promotion, error)
	// ForgetPromotion drops Terraform's record of a promotion. VEW does not
	// undo a release.
	ForgetPromotion(context.Context, string, string, string, string) error
}

// VersionClient keeps Publishing version read and promote scopes on separate transports.
type VersionClient struct {
	promote *vew.Transport
	read    *vew.Transport
}

var (
	_ VersionReadAPI = (*VersionClient)(nil)
	_ PromotionAPI   = (*VersionClient)(nil)
)

// NewVersionClient constructs a version client from version.promote and version.read transports.
func NewVersionClient(promote, read *vew.Transport) *VersionClient {
	return &VersionClient{promote: promote, read: read}
}

func versionSegments(projectID, productID, versionID, stage string) ([]string, error) {
	segments, err := productSegments(projectID, productID, true)
	if err != nil {
		return nil, err
	}
	segments = append(segments, "versions")
	if versionID == "" && stage == "" {
		return segments, nil
	}
	if strings.TrimSpace(versionID) == "" {
		return nil, errors.New("VEW product version ID must not be empty")
	}
	if !validStage(stage) {
		return nil, errors.New("VEW stage must be DEV, QA, or PROD")
	}
	return append(segments, versionID, "stages", stage), nil
}

func validStage(stage string) bool {
	for _, candidate := range Stages {
		if stage == candidate {
			return true
		}
	}
	return false
}

// ListProductVersions returns every version of a product with its stages.
func (c *VersionClient) ListProductVersions(ctx context.Context, projectID, productID string) ([]ProductVersion, error) {
	segments, err := versionSegments(projectID, productID, "", "")
	if err != nil {
		return nil, err
	}
	response, _, err := c.read.Do(ctx, http.MethodGet, segments, nil, "")
	if err != nil {
		return nil, sanitizeError(err)
	}
	var page struct {
		Versions []ProductVersion `json:"versions"`
	}
	if err := json.Unmarshal(response, &page); err != nil {
		return nil, errors.New("VEW product versions response could not be decoded")
	}
	return page.Versions, nil
}

// GetPromotion reads a version's release to a stage; VEW answers 404 until the
// version is in the stage.
func (c *VersionClient) GetPromotion(ctx context.Context, projectID, productID, versionID, stage string) (Promotion, error) {
	segments, err := versionSegments(projectID, productID, versionID, stage)
	if err != nil {
		return Promotion{}, err
	}
	response, _, err := c.read.Do(ctx, http.MethodGet, segments, nil, "")
	if err != nil {
		return Promotion{}, sanitizeError(err)
	}
	return decodePromotion(response)
}

// PromoteVersion releases a version to a stage. VEW answers 202 while the
// stage's distributions are being created and 200 once all are created.
func (c *VersionClient) PromoteVersion(ctx context.Context, projectID, productID, versionID, stage string) (Promotion, error) {
	segments, err := versionSegments(projectID, productID, versionID, stage)
	if err != nil {
		return Promotion{}, err
	}
	response, _, err := c.promote.Do(ctx, http.MethodPut, segments, nil, "")
	if err != nil {
		return Promotion{}, sanitizeError(err)
	}
	return decodePromotion(response)
}

// ForgetPromotion drops a promotion from Terraform. A release is not undone.
func (c *VersionClient) ForgetPromotion(ctx context.Context, projectID, productID, versionID, stage string) error {
	segments, err := versionSegments(projectID, productID, versionID, stage)
	if err != nil {
		return err
	}
	_, _, err = c.promote.Do(ctx, http.MethodDelete, segments, nil, "")
	if err == nil || vew.IsNotFound(err) {
		return nil
	}
	return sanitizeError(err)
}

func decodePromotion(body []byte) (Promotion, error) {
	var promotion Promotion
	if err := json.Unmarshal(body, &promotion); err != nil || strings.TrimSpace(promotion.VersionID) == "" || strings.TrimSpace(promotion.Stage) == "" {
		return Promotion{}, errors.New("VEW promotion response missing version or stage")
	}
	return promotion, nil
}
