package projectaccounts

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// ListAPI reads all account assignments of a project.
type ListAPI interface {
	ListAccounts(context.Context, string) ([]Account, error)
}

var _ ListAPI = (*Client)(nil)

// ListAccounts returns every account assignment of the project, including inactive ones.
func (c *Client) ListAccounts(ctx context.Context, projectID string) ([]Account, error) {
	segments, err := accountSegments(projectID, "", false)
	if err != nil {
		return nil, err
	}
	response, _, err := c.read.Do(ctx, http.MethodGet, segments, nil, "")
	if err != nil {
		return nil, safeAccountError("list", err)
	}
	var page struct {
		Accounts []Account `json:"accounts"`
	}
	if json.Unmarshal(response, &page) != nil {
		return nil, errors.New("VEW project account list response could not be decoded")
	}
	for _, account := range page.Accounts {
		if strings.TrimSpace(account.ID) == "" {
			return nil, errors.New("VEW project account list response has an account without an ID")
		}
	}
	return page.Accounts, nil
}
