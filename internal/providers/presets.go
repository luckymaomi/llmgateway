package providers

import (
	"fmt"
	"net/url"
	"regexp"
	"time"
)

var presetSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}[a-z0-9]$`)

type ProviderPreset struct {
	ID         string
	Slug       string
	Name       string
	Kind       Kind
	BaseURL    string
	SourceURL  string
	VerifiedAt string
	Models     []ModelPreset
}

type ModelPreset struct {
	PublicName    string
	UpstreamName  string
	DisplayName   string
	Capabilities  []string
	ReasoningMode string
	ContextTokens int64
}

func (c *Catalog) Presets() []ProviderPreset {
	if c == nil {
		return nil
	}
	return cloneProviderPresets(c.presets)
}

func (c *Catalog) Preset(id string) (ProviderPreset, bool) {
	if c == nil {
		return ProviderPreset{}, false
	}
	preset, found := c.presetByID[id]
	return cloneProviderPreset(preset), found
}

func validateProviderPreset(preset ProviderPreset) error {
	if preset.ID == "" || !presetSlugPattern.MatchString(preset.Slug) || preset.Name == "" || preset.Kind == "" || len(preset.Models) == 0 {
		return fmt.Errorf("id, slug, name, kind, and models are required")
	}
	for label, value := range map[string]string{"base URL": preset.BaseURL, "source URL": preset.SourceURL} {
		parsed, err := url.ParseRequestURI(value)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
			return fmt.Errorf("%s must be an absolute HTTPS URL", label)
		}
	}
	if _, err := time.Parse(time.DateOnly, preset.VerifiedAt); err != nil {
		return fmt.Errorf("verified date must use YYYY-MM-DD")
	}
	modelNames := make(map[string]struct{}, len(preset.Models))
	for _, model := range preset.Models {
		if err := validateModelPreset(model); err != nil {
			return err
		}
		if _, found := modelNames[model.PublicName]; found {
			return fmt.Errorf("model public name %q is duplicated", model.PublicName)
		}
		modelNames[model.PublicName] = struct{}{}
	}
	return nil
}

func validateModelPreset(model ModelPreset) error {
	if model.PublicName == "" || model.UpstreamName == "" || model.DisplayName == "" || model.ContextTokens < 1 {
		return fmt.Errorf("model names and context tokens are required")
	}
	allowed := map[string]bool{"streaming": true, "tools": true, "reasoning": true, "structured_output": true}
	seen := make(map[string]struct{}, len(model.Capabilities))
	for _, capability := range model.Capabilities {
		if !allowed[capability] {
			return fmt.Errorf("model capability %q is invalid", capability)
		}
		if _, found := seen[capability]; found {
			return fmt.Errorf("model capability %q is duplicated", capability)
		}
		seen[capability] = struct{}{}
	}
	_, reasoning := seen["reasoning"]
	if reasoning != (model.ReasoningMode != "") {
		return fmt.Errorf("model reasoning capability and mode must agree")
	}
	if model.ReasoningMode != "" && model.ReasoningMode != "toggle" && model.ReasoningMode != "effort" && model.ReasoningMode != "hybrid" {
		return fmt.Errorf("model reasoning mode is invalid")
	}
	return nil
}

func cloneProviderPresets(presets []ProviderPreset) []ProviderPreset {
	result := make([]ProviderPreset, len(presets))
	for index, preset := range presets {
		result[index] = cloneProviderPreset(preset)
	}
	return result
}

func cloneProviderPreset(preset ProviderPreset) ProviderPreset {
	preset.Models = append([]ModelPreset(nil), preset.Models...)
	for index := range preset.Models {
		preset.Models[index].Capabilities = append([]string(nil), preset.Models[index].Capabilities...)
	}
	return preset
}
