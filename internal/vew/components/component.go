package components

// CreateComponentInput is the mutable configuration required to create a component.
type CreateComponentInput struct {
	Name                   string   `json:"componentName"`
	Description            string   `json:"componentDescription"`
	Platform               string   `json:"componentPlatform"`
	SupportedArchitectures []string `json:"componentSupportedArchitectures"`
	SupportedOSVersions    []string `json:"componentSupportedOsVersions"`
}

// UpdateComponentInput contains the one configuration property VEW permits changing.
type UpdateComponentInput struct {
	Description string `json:"componentDescription"`
}

// Component is VEW's component representation.
type Component struct {
	ID                     string   `json:"componentId"`
	Name                   string   `json:"componentName"`
	Description            string   `json:"componentDescription"`
	Platform               string   `json:"componentPlatform"`
	SupportedArchitectures []string `json:"componentSupportedArchitectures"`
	SupportedOSVersions    []string `json:"componentSupportedOsVersions"`
	Status                 string   `json:"status"`
	CreatedAt              string   `json:"createDate"`
	CreatedBy              string   `json:"createdBy"`
	UpdatedAt              string   `json:"lastUpdateDate"`
	UpdatedBy              string   `json:"lastUpdatedBy"`
}
