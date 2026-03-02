package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sipeed/picoclaw/pkg/tools"
)

type Tool struct {
	server *Server

	externalName string
	originalName string

	description string
	parameters  map[string]any
}

func NewTool(server *Server, prefix string, def ToolDef) *Tool {
	if server == nil {
		return nil
	}
	orig := strings.TrimSpace(def.Name)
	if orig == "" {
		return nil
	}

	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = server.ToolPrefix()
	}

	name := sanitizeToolName(prefix + orig)
	if name == "" {
		return nil
	}

	desc := strings.TrimSpace(def.Description)
	if desc == "" {
		desc = orig
	}

	params := def.InputSchema
	if params == nil {
		params = map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
	}

	return &Tool{
		server:       server,
		externalName: name,
		originalName: orig,
		description:  desc,
		parameters:   params,
	}
}

func (t *Tool) Name() string { return t.externalName }

func (t *Tool) Description() string {
	server := ""
	if t.server != nil {
		server = t.server.Name()
	}
	if server == "" {
		return t.description
	}
	return fmt.Sprintf("[mcp:%s] %s", server, t.description)
}

func (t *Tool) Parameters() map[string]any { return t.parameters }

func (t *Tool) ParallelPolicy() tools.ToolParallelPolicy { return tools.ToolParallelSerialOnly }

func (t *Tool) SupportsConcurrentExecution() bool { return false }

func (t *Tool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	if t == nil || t.server == nil {
		return tools.ErrorResult("mcp tool not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	result, err := t.server.CallTool(ctx, t.originalName, args)
	if err != nil {
		return tools.ErrorResult(err.Error()).WithError(err)
	}
	if result == nil {
		return tools.ErrorResult("mcp tool returned nil result").WithError(fmt.Errorf("nil mcp result"))
	}

	// Best-effort: concatenate textual blocks. When content types are not text,
	// fall back to JSON for preservation (the LLM can still inspect it).
	var parts []string
	for _, block := range result.Content {
		text := extractTextBlock(block)
		if strings.TrimSpace(text) == "" {
			raw, _ := json.Marshal(block)
			if len(raw) > 0 {
				text = string(raw)
			}
		}
		if strings.TrimSpace(text) != "" {
			parts = append(parts, strings.TrimSpace(text))
		}
	}

	out := strings.TrimSpace(strings.Join(parts, "\n"))
	if out == "" && result.StructuredContent != nil {
		// Some servers may return structured_content without text blocks.
		raw, _ := json.Marshal(result.StructuredContent)
		out = strings.TrimSpace(string(raw))
	}
	if out == "" {
		out = "(no output)"
	}

	if result.IsError {
		return tools.ErrorResult(out).WithError(fmt.Errorf("mcp tool error"))
	}

	return tools.SilentResult(out)
}

// extractTextBlock attempts to extract a plain text string from an MCP content block
// without depending on concrete transport structs.
func extractTextBlock(block any) string {
	if block == nil {
		return ""
	}

	// Try common JSON field shapes: {"text":"..."}.
	raw, err := json.Marshal(block)
	if err != nil || len(raw) == 0 {
		return ""
	}

	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	if v, ok := m["text"]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
