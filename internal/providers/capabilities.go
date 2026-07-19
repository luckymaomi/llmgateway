package providers

type Capabilities struct {
	Chat               bool
	Models             bool
	Responses          bool
	Streaming          bool
	Tools              bool
	ToolStreaming      bool
	ToolChoiceNone     bool
	ToolChoiceAuto     bool
	ToolChoiceRequired bool
	ToolChoiceNamed    bool
	StrictTools        bool
	ImageInput         bool
	JSONOutput         bool
	MessageName        bool
	ReasoningToggle    bool
	ReasoningEffort    bool
	ReasoningContent   bool
	ReasoningReplay    bool
	ResponseUsage      bool
	StreamUsage        bool
	ResponseRequestID  bool
}

func NarrowOpenAICompatibleCapabilities() Capabilities {
	return Capabilities{
		Chat:               true,
		Models:             true,
		Streaming:          true,
		Tools:              true,
		ToolStreaming:      true,
		ToolChoiceNone:     true,
		ToolChoiceAuto:     true,
		ToolChoiceRequired: true,
		ToolChoiceNamed:    true,
		StrictTools:        true,
		JSONOutput:         true,
		MessageName:        true,
		ReasoningEffort:    true,
		ResponseUsage:      true,
		StreamUsage:        true,
	}
}
