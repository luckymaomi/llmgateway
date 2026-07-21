package providers

import (
	"fmt"
	"sort"
)

type AdapterOptions struct {
	BaseURL      string
	Capabilities Capabilities
}

type AdapterBuilder func(AdapterOptions) (Adapter, error)

type Definition struct {
	Kind        Kind
	DisplayName string
	Build       AdapterBuilder
}

type KindInfo struct {
	Kind        Kind
	DisplayName string
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
		if _, exists := catalog.definitions[definition.Kind]; exists {
			return nil, fmt.Errorf("Provider kind %q is registered more than once", definition.Kind)
		}
		catalog.definitions[definition.Kind] = definition
		catalog.kinds = append(catalog.kinds, KindInfo{Kind: definition.Kind, DisplayName: definition.DisplayName})
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
	return append([]KindInfo(nil), c.kinds...)
}

var defaultCatalog = mustCatalog([]Definition{
	{
		Kind: KindOpenAICompatible, DisplayName: "OpenAI-compatible",
		Build: func(options AdapterOptions) (Adapter, error) {
			return NewOpenAICompatible(OpenAICompatibleOptions{BaseURL: options.BaseURL, Capabilities: options.Capabilities})
		},
	},
	{
		Kind: KindZhipu, DisplayName: "智谱 GLM",
		Build: func(options AdapterOptions) (Adapter, error) { return NewZhipuWithBaseURL(options.BaseURL) },
	},
	{
		Kind: KindAgnes, DisplayName: "Agnes",
		Build: func(options AdapterOptions) (Adapter, error) { return NewAgnesWithBaseURL(options.BaseURL) },
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
