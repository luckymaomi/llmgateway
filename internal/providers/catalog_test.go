package providers

import "testing"

func TestDefaultCatalogBuildsEveryPublishedProviderKind(t *testing.T) {
	catalog := DefaultCatalog()
	kinds := catalog.Kinds()
	if len(kinds) != 3 {
		t.Fatalf("Provider kinds = %#v", kinds)
	}
	for _, info := range kinds {
		adapter, err := catalog.Build(info.Kind, AdapterOptions{
			BaseURL: "https://provider.example.test/v1", Capabilities: NarrowOpenAICompatibleCapabilities(),
		})
		if err != nil {
			t.Fatalf("Build(%q) error = %v", info.Kind, err)
		}
		if adapter.Kind() != info.Kind {
			t.Fatalf("Build(%q) returned kind %q", info.Kind, adapter.Kind())
		}
	}
}
