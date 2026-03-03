package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type AgentInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	Model     string `json:"model,omitempty"`
	Workspace string `json:"workspace,omitempty"`
}

type AgentsListTool struct {
	list func() []AgentInfo
}

func NewAgentsListTool(list func() []AgentInfo) *AgentsListTool {
	return &AgentsListTool{list: list}
}

func (t *AgentsListTool) Name() string {
	return "agents_list"
}

func (t *AgentsListTool) ParallelPolicy() ToolParallelPolicy {
	return ToolParallelReadOnly
}

func (t *AgentsListTool) Description() string {
	return "List available agent IDs (and basic metadata) that you can hand off to. " +
		"Use this before calling handoff if you are not sure which agent_name/agent_id exists."
}

func (t *AgentsListTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"include_workspace": map[string]any{
				"type":        "boolean",
				"description": "Include each agent's workspace path (default false).",
			},
		},
	}
}

func (t *AgentsListTool) Execute(_ context.Context, args map[string]any) *ToolResult {
	includeWorkspace, err := parseBoolArg(args, "include_workspace", false)
	if err != nil {
		return ErrorResult(err.Error())
	}

	var agents []AgentInfo
	if t != nil && t.list != nil {
		agents = t.list()
	}

	// Normalize output: stable sorting and trimmed fields.
	out := make([]AgentInfo, 0, len(agents))
	for _, a := range agents {
		id := strings.TrimSpace(a.ID)
		if id == "" {
			continue
		}
		item := AgentInfo{
			ID:    id,
			Name:  strings.TrimSpace(a.Name),
			Model: strings.TrimSpace(a.Model),
		}
		if includeWorkspace {
			item.Workspace = strings.TrimSpace(a.Workspace)
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })

	payload := map[string]any{
		"count":  len(out),
		"agents": out,
	}

	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to encode agents_list payload: %v", err))
	}
	return SilentResult(string(data))
}
