package main

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/plugin"
	"github.com/schwitzd/terraform-provider-garage/garage"
)

func TestMainInvokesPluginServe(t *testing.T) {
	t.Helper()

	var capturedOpts *plugin.ServeOpts
	originalServe := pluginServe
	pluginServe = func(opts *plugin.ServeOpts) {
		capturedOpts = opts
	}
	t.Cleanup(func() {
		pluginServe = originalServe
	})

	main()

	if capturedOpts == nil {
		t.Fatalf("expected ServeOpts to be passed to pluginServe")
	}
	if capturedOpts.ProviderFunc == nil {
		t.Fatalf("expected ProviderFunc to be set")
	}

	gotProvider := capturedOpts.ProviderFunc()
	if gotProvider == nil {
		t.Fatalf("expected provider instance")
	}

	wantProvider := garage.Provider()

	if len(gotProvider.Schema) != len(wantProvider.Schema) {
		t.Fatalf("provider schema size mismatch got=%d want=%d", len(gotProvider.Schema), len(wantProvider.Schema))
	}
	for key := range wantProvider.Schema {
		if _, ok := gotProvider.Schema[key]; !ok {
			t.Fatalf("expected schema to contain key %q", key)
		}
	}

	if len(gotProvider.ResourcesMap) != len(wantProvider.ResourcesMap) {
		t.Fatalf("provider resources size mismatch got=%d want=%d", len(gotProvider.ResourcesMap), len(wantProvider.ResourcesMap))
	}
	for key := range wantProvider.ResourcesMap {
		if _, ok := gotProvider.ResourcesMap[key]; !ok {
			t.Fatalf("expected resources to contain key %q", key)
		}
	}
}
