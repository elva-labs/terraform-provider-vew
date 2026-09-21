package main

import (
	"context"
	"log"

	"github.com/elva-labs/terraform-provider-vew/internal/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
)

var version = "dev"

func main() {
	err := providerserver.Serve(
		context.Background(),
		provider.New(version),
		providerserver.ServeOpts{Address: "registry.terraform.io/elva-labs/vew"},
	)
	if err != nil {
		log.Fatal(err)
	}
}
