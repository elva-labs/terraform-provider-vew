package providerdata

import "github.com/elva-labs/terraform-provider-vew/internal/vew/components"

// Data is shared by provider resources and data sources.
type Data struct {
	Components components.API
	Waiter     any
}
