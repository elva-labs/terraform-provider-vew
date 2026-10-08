package projectaccounts

import "time"

// Account is the safe, project-scoped view returned by the Projects S2S API.
// It deliberately has no field for the internal account parameters map.
type Account struct {
	ID                         string `json:"accountId"`
	ProjectID                  string `json:"projectId"`
	AWSAccountID               string `json:"awsAccountId"`
	Name                       string `json:"name"`
	Description                string `json:"description"`
	AccountType                string `json:"accountType"`
	TechnologyID               string `json:"technologyId"`
	Stage                      string `json:"stage"`
	Region                     string `json:"region"`
	Status                     string `json:"status"`
	LastOnboardingResult       string `json:"lastOnboardingResult"`
	LastOnboardingErrorMessage string `json:"lastOnboardingError"`
	OnboardingRevision         string `json:"onboardingRevision"`
	// OnboardedAt is when onboarding first succeeded; VEW sets it once and never clears it.
	OnboardedAt string        `json:"onboardedAt"`
	CreatedAt   time.Time     `json:"createDate"`
	UpdatedAt   time.Time     `json:"lastUpdateDate"`
	RetryAfter  time.Duration `json:"-"`
}

// AccountInput is the complete desired account assignment sent to VEW.
type AccountInput struct {
	AWSAccountID string `json:"awsAccountId"`
	AccountType  string `json:"accountType"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	TechnologyID string `json:"technologyId"`
	Stage        string `json:"stage"`
	Region       string `json:"region"`
	// OnboardingRevision is opaque to VEW; omitted when unset.
	OnboardingRevision string `json:"onboardingRevision,omitempty"`
}

// UpdateAccountInput is the mutable portion of an account assignment. Project
// and AWS account IDs are fixed by the account's identity and are omitted.
type UpdateAccountInput struct {
	AccountType  string `json:"accountType"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	TechnologyID string `json:"technologyId"`
	Stage        string `json:"stage"`
	Region       string `json:"region"`
	// OnboardingRevision re-runs onboarding of an unchanged configuration when it
	// differs from the stored revision. Omitted when unset, which keeps the stored one.
	OnboardingRevision string `json:"onboardingRevision,omitempty"`
}

// ActionResult identifies an accepted asynchronous account operation.
type ActionResult struct {
	ID         string
	RetryAfter time.Duration
}
