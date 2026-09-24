package recipes

// CreateRecipeInput contains the fields accepted by the recipe create API.
type CreateRecipeInput struct {
	Name         string `json:"recipeName"`
	Description  string `json:"recipeDescription"`
	Platform     string `json:"recipePlatform"`
	Architecture string `json:"recipeArchitecture"`
	OSVersion    string `json:"recipeOsVersion"`
}

// Recipe is the project-scoped recipe returned by VEW.
type Recipe struct {
	ID           string `json:"recipeId"`
	Name         string `json:"recipeName"`
	Description  string `json:"recipeDescription"`
	Platform     string `json:"recipePlatform"`
	Architecture string `json:"recipeArchitecture"`
	OSVersion    string `json:"recipeOsVersion"`
	Status       string `json:"status"`
	CreatedAt    string `json:"createDate"`
	CreatedBy    string `json:"createdBy"`
	UpdatedAt    string `json:"lastUpdateDate"`
	UpdatedBy    string `json:"lastUpdatedBy"`
}
