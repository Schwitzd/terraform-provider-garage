package main

import (
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/plugin"
	"github.com/schwitzd/terraform-provider-garage/garage"
)

func main() {
	pluginServe(&plugin.ServeOpts{
		ProviderFunc: func() *schema.Provider {
			return garage.Provider()
		},
	})
}

var pluginServe = plugin.Serve
