package projectaccess

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
)

type BootstrapAPI interface {
	AssignClient(context.Context, string, string) (ClientAssignment, error)
}
type BootstrapClient struct{ transport *vew.Transport }

func NewBootstrapClient(transport *vew.Transport) *BootstrapClient {
	return &BootstrapClient{transport: transport.WithoutRetries()}
}
func (c *BootstrapClient) AssignClient(ctx context.Context, projectID, clientID string) (ClientAssignment, error) {
	path, err := segments(projectID, "clients", clientID)
	if err != nil {
		return ClientAssignment{}, err
	}
	body, err := call(ctx, c.transport, http.MethodPut, path, nil, "")
	if err != nil {
		return ClientAssignment{}, err
	}
	var envelope struct {
		Assignment *ClientAssignment `json:"assignment"`
	}
	if json.Unmarshal(body, &envelope) != nil || envelope.Assignment == nil {
		return ClientAssignment{}, errors.New("VEW bootstrap response missing a valid assignment")
	}
	return *envelope.Assignment, nil
}
