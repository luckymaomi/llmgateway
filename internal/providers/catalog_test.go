package providers

import (
	"strings"
	"testing"
)

func TestDefaultCatalogBuildsEveryPublishedProviderKind(t *testing.T) {
	catalog := DefaultCatalog()
	kinds := catalog.Kinds()
	if len(kinds) != 4 {
		t.Fatalf("Provider kinds = %#v", kinds)
	}
	for _, info := range kinds {
		if info.Contract.ReferenceURL == "" || info.Contract.ContractSnapshot == "" || info.Contract.VerifiedAt == "" ||
			len(info.Contract.VerifiedModels) == 0 || len(info.Contract.LiveCapabilities) == 0 {
			t.Fatalf("Provider kind %q has incomplete contract metadata: %#v", info.Kind, info.Contract)
		}
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
	if kinds[0].Kind != KindAgnes || kinds[0].Contract.Status != VerificationVerified {
		t.Fatalf("Agnes contract = %#v", kinds[0])
	}
	gemini := kinds[1]
	if gemini.Kind != KindGemini || gemini.Contract.Status != VerificationVerified {
		t.Fatalf("Gemini contract must preserve the live verified fact: %#v", gemini)
	}
	kinds[0].Contract.VerifiedModels[0] = "mutated"
	if DefaultCatalog().Kinds()[0].Contract.VerifiedModels[0] == "mutated" {
		t.Fatal("Kinds() exposed mutable catalog contract state")
	}
}

func TestDefaultCatalogPublishesIndependentInstallableProviderPresets(t *testing.T) {
	presets := DefaultCatalog().Presets()
	if len(presets) != 4 {
		t.Fatalf("Provider presets = %#v, want four verified entry points", presets)
	}
	for _, preset := range presets {
		if preset.ID == "" || preset.Kind == "" || preset.BaseURL == "" || len(preset.Models) == 0 {
			t.Fatalf("Provider preset is incomplete: %#v", preset)
		}
	}
	presets[0].Models[0].Capabilities[0] = "mutated"
	reloaded, found := DefaultCatalog().Preset(presets[0].ID)
	if !found || reloaded.Models[0].Capabilities[0] == "mutated" {
		t.Fatal("Provider preset catalog exposed mutable capability facts")
	}
}

func TestCatalogRejectsUntraceableContract(t *testing.T) {
	_, err := NewCatalog([]Definition{{
		Kind: "missing-contract", DisplayName: "Missing", Build: func(AdapterOptions) (Adapter, error) { return NewAgnes(), nil },
	}})
	if err == nil || !strings.Contains(err.Error(), "reference URL") {
		t.Fatalf("NewCatalog() error = %v, want contract rejection", err)
	}
}
