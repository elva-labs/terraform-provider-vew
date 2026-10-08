package products

// CreateProductInput is the configuration accepted when a product is created.
// Its type and technology are fixed for the product's lifetime.
type CreateProductInput struct {
	Name         string `json:"productName"`
	Type         string `json:"productType"`
	Description  string `json:"productDescription"`
	TechnologyID string `json:"technologyId"`
	// Scope is PROGRAM (the default, omitted) or PLATFORM: a product the releasing
	// project builds once and VEW distributes to every project's accounts.
	Scope string `json:"scope,omitempty"`
}

// UpdateProductInput is the mutable configuration of a product.
type UpdateProductInput struct {
	Name        string `json:"productName"`
	Description string `json:"productDescription"`
}

// Product is the canonical project-scoped product record returned by VEW.
type Product struct {
	ProjectID            string   `json:"projectId"`
	ID                   string   `json:"productId"`
	Name                 string   `json:"productName"`
	Type                 string   `json:"productType"`
	Description          string   `json:"productDescription"`
	TechnologyID         string   `json:"technologyId"`
	TechnologyName       string   `json:"technologyName"`
	Status               string   `json:"status"`
	RecommendedVersionID string   `json:"recommendedVersionId"`
	AvailableStages      []string `json:"availableStages"`
	Scope                string   `json:"scope"`
	CreatedAt            string   `json:"createDate"`
	UpdatedAt            string   `json:"lastUpdateDate"`
}

// Archived reports whether the product is being or has been archived. VEW keeps
// returning archived products, but they can no longer be used or changed.
func (p Product) Archived() bool {
	return p.Status == "ARCHIVING" || p.Status == "ARCHIVED"
}
