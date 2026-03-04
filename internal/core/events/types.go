package events

// Type is the canonical event taxonomy string used in run/tool traces and event sinks.
// Keep values stable for backward compatibility with stored JSONL traces.
type Type string

const (
	RunStart  Type = "run.start"
	RunResume Type = "run.resume"
	RunEnd    Type = "run.end"
	RunError  Type = "run.error"

	LLMRequest  Type = "llm.request"
	LLMResponse Type = "llm.response"

	ToolBatch Type = "tool.batch"

	ToolStart     Type = "tool.start"
	ToolEnd       Type = "tool.end"
	ToolExecuted  Type = "tool.executed"
	ToolConfirmed Type = "tool.confirmed"
)
