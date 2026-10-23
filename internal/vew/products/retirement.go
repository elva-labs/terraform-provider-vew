package products

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
)

// Retirement is a product version's retirement in every account it was
// distributed to.
type Retirement struct {
	ProjectID     string         `json:"projectId"`
	ProductID     string         `json:"productId"`
	VersionID     string         `json:"versionId"`
	VersionName   string         `json:"versionName"`
	Status        string         `json:"status"`
	RetireReason  string         `json:"retireReason"`
	Distributions []Distribution `json:"distributions"`
}

// RetireRequest is the optional body of a retirement.
type RetireRequest struct {
	Reason string `json:"reason,omitempty"`
	// Force retires even with workbenches still on the version; VEW refuses
	// with 409 VERSION_IN_USE otherwise.
	Force bool `json:"force,omitempty"`
}

// RetirementAPI manages a version's retirement.
type RetirementAPI interface {
	// GetRetirement reads a retirement; VEW answers 404 while the version is
	// not retiring or retired.
	GetRetirement(context.Context, string, string, string) (Retirement, error)
	// RetireVersion retires a version. Repeating it returns the current state
	// and never retires twice.
	RetireVersion(context.Context, string, string, string, RetireRequest) (Retirement, error)
	// ForgetRetirement drops Terraform's record of a retirement. VEW does not
	// restore the version.
	ForgetRetirement(context.Context, string, string, string) error
}

// RetirementClient keeps the version.retire and version.read scopes on
// separate transports.
type RetirementClient struct {
	retire *vew.Transport
	read   *vew.Transport
}

var _ RetirementAPI = (*RetirementClient)(nil)

// NewRetirementClient constructs a retirement client from version.retire and
// version.read transports.
func NewRetirementClient(retire, read *vew.Transport) *RetirementClient {
	return &RetirementClient{retire: retire, read: read}
}

func retirementSegments(projectID, productID, versionID string) ([]string, error) {
	segments, err := productSegments(projectID, productID, true)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(versionID) == "" {
		return nil, errors.New("VEW product version ID must not be empty")
	}
	return append(segments, "versions", versionID, "retirement"), nil
}

// GetRetirement reads a version's retirement.
func (c *RetirementClient) GetRetirement(ctx context.Context, projectID, productID, versionID string) (Retirement, error) {
	segments, err := retirementSegments(projectID, productID, versionID)
	if err != nil {
		return Retirement{}, err
	}
	response, _, err := c.read.Do(ctx, http.MethodGet, segments, nil, "")
	if err != nil {
		return Retirement{}, sanitizeError(err)
	}
	return decodeRetirement(response)
}

// RetireVersion retires a version in every account. VEW answers 202 while
// distributions are being retired and 200 once all are retired.
func (c *RetirementClient) RetireVersion(ctx context.Context, projectID, productID, versionID string, request RetireRequest) (Retirement, error) {
	segments, err := retirementSegments(projectID, productID, versionID)
	if err != nil {
		return Retirement{}, err
	}
	body, err := json.Marshal(request)
	if err != nil {
		return Retirement{}, errors.New("VEW retirement request could not be encoded")
	}
	response, _, err := c.retire.Do(ctx, http.MethodPut, segments, body, "")
	if err != nil {
		return Retirement{}, sanitizeError(err)
	}
	return decodeRetirement(response)
}

// ForgetRetirement drops a retirement from Terraform. The version stays retired.
func (c *RetirementClient) ForgetRetirement(ctx context.Context, projectID, productID, versionID string) error {
	segments, err := retirementSegments(projectID, productID, versionID)
	if err != nil {
		return err
	}
	_, _, err = c.retire.Do(ctx, http.MethodDelete, segments, nil, "")
	if err == nil || vew.IsNotFound(err) {
		return nil
	}
	return sanitizeError(err)
}

func decodeRetirement(body []byte) (Retirement, error) {
	var retirement Retirement
	if err := json.Unmarshal(body, &retirement); err != nil || strings.TrimSpace(retirement.VersionID) == "" || strings.TrimSpace(retirement.Status) == "" {
		return Retirement{}, errors.New("VEW retirement response missing version or status")
	}
	return retirement, nil
}
