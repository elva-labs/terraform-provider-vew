package technologies

// TechnologyInput is the declarative configuration accepted by the Projects API.
type TechnologyInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Technology is the canonical project-scoped technology record returned by VEW.
type Technology struct {
	ID          string `json:"technologyId"`
	ProjectID   string `json:"projectId"`
	Name        string `json:"name"`
	Description string `json:"description"`
	CreatedAt   string `json:"createDate"`
	UpdatedAt   string `json:"lastUpdateDate"`
}
