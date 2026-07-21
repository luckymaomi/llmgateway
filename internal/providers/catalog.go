package providers

import (
	"fmt"
	"net/url"
	"sort"
	"time"
)

type AdapterOptions struct {
	BaseURL      string
	Capabilities Capabilities
}

type AdapterBuilder func(AdapterOptions) (Adapter, error)

type Definition struct {
	Kind        Kind
	DisplayName string
	Contract    ContractInfo
	Build       AdapterBuilder
}

type KindInfo struct {
	Kind        Kind
	DisplayName string
	Contract    ContractInfo
}

type VerificationStatus string

const (
	VerificationVerified VerificationStatus = "verified"
	VerificationDegraded VerificationStatus = "degraded"
)

type ContractInfo struct {
	ReferenceURL      string
	ContractSnapshot  string
	VerifiedAt        string
	ReferenceProvider string
	VerifiedModels    []string
	LiveCapabilities  []string
	Status            VerificationStatus
}

type Catalog struct {
	definitions map[Kind]Definition
	kinds       []KindInfo
}

func NewCatalog(definitions []Definition) (*Catalog, error) {
	if len(definitions) == 0 {
		return nil, fmt.Errorf("at least one Provider definition is required")
	}
	catalog := &Catalog{definitions: make(map[Kind]Definition, len(definitions)), kinds: make([]KindInfo, 0, len(definitions))}
	for _, definition := range definitions {
		if definition.Kind == "" || definition.DisplayName == "" || definition.Build == nil {
			return nil, fmt.Errorf("Provider definition requires kind, display name, and builder")
		}
		if err := validateContractInfo(definition.Contract); err != nil {
			return nil, fmt.Errorf("Provider kind %q contract: %w", definition.Kind, err)
		}
		if _, exists := catalog.definitions[definition.Kind]; exists {
			return nil, fmt.Errorf("Provider kind %q is registered more than once", definition.Kind)
		}
		definition.Contract = cloneContractInfo(definition.Contract)
		catalog.definitions[definition.Kind] = definition
		catalog.kinds = append(catalog.kinds, KindInfo{Kind: definition.Kind, DisplayName: definition.DisplayName, Contract: cloneContractInfo(definition.Contract)})
	}
	sort.Slice(catalog.kinds, func(i, j int) bool { return catalog.kinds[i].Kind < catalog.kinds[j].Kind })
	return catalog, nil
}

func (c *Catalog) Build(kind Kind, options AdapterOptions) (Adapter, error) {
	if c == nil {
		return nil, fmt.Errorf("Provider catalog is required")
	}
	definition, found := c.definitions[kind]
	if !found {
		return nil, fmt.Errorf("unsupported Provider kind %q", kind)
	}
	return definition.Build(options)
}

func (c *Catalog) Supports(kind Kind) bool {
	if c == nil {
		return false
	}
	_, found := c.definitions[kind]
	return found
}

func (c *Catalog) Kinds() []KindInfo {
	if c == nil {
		return nil
	}
	result := make([]KindInfo, len(c.kinds))
	for index, info := range c.kinds {
		result[index] = KindInfo{Kind: info.Kind, DisplayName: info.DisplayName, Contract: cloneContractInfo(info.Contract)}
	}
	return result
}

func validateContractInfo(info ContractInfo) error {
	reference, err := url.ParseRequestURI(info.ReferenceURL)
	if err != nil || reference.Scheme != "https" || reference.Host == "" {
		return fmt.Errorf("reference URL must be an absolute HTTPS URL")
	}
	if info.ContractSnapshot == "" {
		return fmt.Errorf("contract snapshot is required")
	}
	if _, err := time.Parse(time.DateOnly, info.VerifiedAt); err != nil {
		return fmt.Errorf("verified date must use YYYY-MM-DD")
	}
	if len(info.VerifiedModels) == 0 || len(info.LiveCapabilities) == 0 {
		return fmt.Errorf("verified models and live capabilities are required")
	}
	if info.Status != VerificationVerified && info.Status != VerificationDegraded {
		return fmt.Errorf("verification status must be verified or degraded")
	}
	for label, values := range map[string][]string{"verified models": info.VerifiedModels, "live capabilities": info.LiveCapabilities} {
		seen := make(map[string]struct{}, len(values))
		for _, value := range values {
			if value == "" {
				return fmt.Errorf("%s cannot contain an empty value", label)
			}
			if _, found := seen[value]; found {
				return fmt.Errorf("%s contains duplicate %q", label, value)
			}
			seen[value] = struct{}{}
		}
	}
	return nil
}

func cloneContractInfo(info ContractInfo) ContractInfo {
	info.VerifiedModels = append([]string(nil), info.VerifiedModels...)
	info.LiveCapabilities = append([]string(nil), info.LiveCapabilities...)
	return info
}

var defaultCatalog = mustCatalog([]Definition{
	{
		Kind: KindOpenAICompatible, DisplayName: "OpenAI-compatible",
		Contract: ContractInfo{
			ReferenceURL: "https://api-docs.siliconflow.cn/docs/api/chat-completions-post", ContractSnapshot: "2026-07-22",
			VerifiedAt: "2026-07-22", ReferenceProvider: "SiliconFlow", VerifiedModels: []string{"Qwen/Qwen3.5-9B"},
			LiveCapabilities: []string{"models", "chat", "responses", "stream", "tools", "reasoning", "usage", "error", "cancel"}, Status: VerificationVerified,
		},
		Build: func(options AdapterOptions) (Adapter, error) {
			return NewOpenAICompatible(OpenAICompatibleOptions{BaseURL: options.BaseURL, Capabilities: options.Capabilities})
		},
	},
	{
		Kind: KindZhipu, DisplayName: "智谱 GLM",
		Contract: ContractInfo{
			ReferenceURL: "https://docs.bigmodel.cn/cn/guide/develop/http/introduction", ContractSnapshot: "2026-07-22",
			VerifiedAt: "2026-07-22", VerifiedModels: []string{"glm-5.2"},
			LiveCapabilities: []string{"models", "chat", "stream", "tools", "reasoning", "usage", "quota_error", "priority_takeover"}, Status: VerificationVerified,
		},
		Build: func(options AdapterOptions) (Adapter, error) { return NewZhipuWithBaseURL(options.BaseURL) },
	},
	{
		Kind: KindAgnes, DisplayName: "Agnes",
		Contract: ContractInfo{
			ReferenceURL: "https://apihub.agnes-ai.com/v1", ContractSnapshot: "2026-07-22 live API wire",
			VerifiedAt: "2026-07-22", VerifiedModels: []string{"agnes-2.0-flash"},
			LiveCapabilities: []string{"models", "chat", "stream", "tools", "reasoning", "usage", "cancel"}, Status: VerificationVerified,
		},
		Build: func(options AdapterOptions) (Adapter, error) { return NewAgnesWithBaseURL(options.BaseURL) },
	},
	{
		Kind: KindGemini, DisplayName: "Google Gemini",
		Contract: ContractInfo{
			ReferenceURL: "https://ai.google.dev/gemini-api/docs/openai", ContractSnapshot: "2026-07-22",
			VerifiedAt: "2026-07-22", VerifiedModels: []string{"gemini-3.5-flash"},
			LiveCapabilities: []string{"models", "chat", "tools", "reasoning", "usage", "retryable_error", "thought_signature", "signed_tool_replay"}, Status: VerificationVerified,
		},
		Build: func(options AdapterOptions) (Adapter, error) { return NewGeminiWithBaseURL(options.BaseURL) },
	},
})

func DefaultCatalog() *Catalog {
	return defaultCatalog
}

func mustCatalog(definitions []Definition) *Catalog {
	catalog, err := NewCatalog(definitions)
	if err != nil {
		panic(err)
	}
	return catalog
}
