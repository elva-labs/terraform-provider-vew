package images

import "time"

// ActionResult identifies the image accepted for an asynchronous build.
type ActionResult struct {
	ID         string
	RetryAfter time.Duration
}

// Image is one project-scoped image build returned by VEW.
type Image struct {
	ID         string        `json:"imageId"`
	ProjectID  string        `json:"projectId"`
	PipelineID string        `json:"pipelineId"`
	Status     string        `json:"status"`
	UpstreamID *string       `json:"imageUpstreamId"`
	RetryAfter time.Duration `json:"-"`
}
