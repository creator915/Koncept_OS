package llm

type Message struct {
	Role string `json:"role"`
	// Content is always emitted (no omitempty). DeepSeek's API rejects
	// messages where the field is absent — including assistant turns with
	// only tool_calls AND tool turns whose command produced no output
	// (e.g., a successful `mv` or `mkdir`). Sending `"content": ""` is
	// always accepted; sending no field at all is not.
	Content          string     `json:"content"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
}

type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}

type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ToolSpec struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}
