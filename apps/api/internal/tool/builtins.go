package tool

type builtinTool struct {
	binding           Binding
	defaultEnabled    bool
	defaultRuleID     string
	runtimeInjectable bool // The application may supply a Binding with runtime dependencies.
}

func builtinTools() []builtinTool {
	return []builtinTool{
		{binding: CalculatorTool(), defaultEnabled: true},
		{binding: CurrentTimeTool(), defaultEnabled: true},
		{binding: WebSearchTool(nil), defaultEnabled: true, defaultRuleID: "builtin-web-search-read", runtimeInjectable: true},
	}
}
