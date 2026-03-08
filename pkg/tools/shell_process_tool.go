package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type ProcessTool struct {
	processes *ProcessManager
}

func NewProcessTool(processes *ProcessManager) *ProcessTool {
	return &ProcessTool{processes: processes}
}

func (t *ProcessTool) Name() string {
	return "process"
}

func (t *ProcessTool) Description() string {
	return "Manage background exec sessions: list, poll, log, write, kill, clear, remove."
}

func (t *ProcessTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"list", "poll", "log", "write", "kill", "clear", "remove"},
				"description": "Process action",
			},
			"session_id": map[string]any{
				"type":        "string",
				"description": "Session ID returned by exec background mode",
			},
			"data": map[string]any{
				"type":        "string",
				"description": "Input data for write action",
			},
			"eof": map[string]any{
				"type":        "boolean",
				"description": "Close stdin after write",
			},
			"offset": map[string]any{
				"type":        "integer",
				"description": "Log line offset",
				"minimum":     0.0,
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Max log lines to return",
				"minimum":     0.0,
			},
			"timeout_ms": map[string]any{
				"type":        "integer",
				"description": "Poll wait timeout in milliseconds",
				"minimum":     0.0,
			},
		},
		"required": []string{"action"},
	}
}

func (t *ProcessTool) Execute(_ context.Context, args map[string]any) *ToolResult {
	if t.processes == nil {
		return ErrorResult("process manager not configured")
	}

	action, ok := getStringArg(args, "action")
	if !ok || strings.TrimSpace(action) == "" {
		return ErrorResult("action is required")
	}
	action = strings.ToLower(strings.TrimSpace(action))

	switch action {
	case "list":
		sessions := t.processes.ListSnapshots()
		return marshalSilentJSON(map[string]any{
			"count":    len(sessions),
			"sessions": sessions,
		})
	case "poll":
		sessionID, ok := getStringArg(args, "session_id")
		if !ok || strings.TrimSpace(sessionID) == "" {
			return ErrorResult("session_id is required for poll")
		}
		timeoutMS, err := parseOptionalIntArg(args, "timeout_ms", 0, 0, 5*60*1000)
		if err != nil {
			return ErrorResult(err.Error())
		}
		result, err := t.processes.Poll(strings.TrimSpace(sessionID), time.Duration(timeoutMS)*time.Millisecond)
		if err != nil {
			return ErrorResult(err.Error())
		}
		return marshalSilentJSON(result)
	case "log":
		sessionID, ok := getStringArg(args, "session_id")
		if !ok || strings.TrimSpace(sessionID) == "" {
			return ErrorResult("session_id is required for log")
		}
		offset, err := parseOptionalIntArg(args, "offset", 0, 0, 1_000_000)
		if err != nil {
			return ErrorResult(err.Error())
		}
		limit, err := parseOptionalIntArg(args, "limit", 0, 0, 1_000_000)
		if err != nil {
			return ErrorResult(err.Error())
		}
		_, hasOffset := args["offset"]
		_, hasLimit := args["limit"]
		useDefaultTail := !hasOffset && !hasLimit
		result, err := t.processes.Log(strings.TrimSpace(sessionID), offset, limit, useDefaultTail)
		if err != nil {
			return ErrorResult(err.Error())
		}
		return marshalSilentJSON(result)
	case "write":
		sessionID, ok := getStringArg(args, "session_id")
		if !ok || strings.TrimSpace(sessionID) == "" {
			return ErrorResult("session_id is required for write")
		}
		data, _ := getStringArg(args, "data")
		eof, err := parseBoolArg(args, "eof", false)
		if err != nil {
			return ErrorResult(err.Error())
		}
		if err := t.processes.Write(strings.TrimSpace(sessionID), data, eof); err != nil {
			return ErrorResult(err.Error())
		}
		return marshalSilentJSON(map[string]any{
			"status":     "ok",
			"action":     "write",
			"session_id": strings.TrimSpace(sessionID),
		})
	case "kill":
		sessionID, ok := getStringArg(args, "session_id")
		if !ok || strings.TrimSpace(sessionID) == "" {
			return ErrorResult("session_id is required for kill")
		}
		signaled, err := t.processes.Kill(strings.TrimSpace(sessionID))
		if err != nil {
			return ErrorResult(err.Error())
		}
		return marshalSilentJSON(map[string]any{
			"status":      "ok",
			"action":      "kill",
			"session_id":  strings.TrimSpace(sessionID),
			"kill_signal": signaled,
		})
	case "clear":
		sessionID, ok := getStringArg(args, "session_id")
		if !ok || strings.TrimSpace(sessionID) == "" {
			return ErrorResult("session_id is required for clear")
		}
		if err := t.processes.Clear(strings.TrimSpace(sessionID)); err != nil {
			return ErrorResult(err.Error())
		}
		return marshalSilentJSON(map[string]any{
			"status":     "ok",
			"action":     "clear",
			"session_id": strings.TrimSpace(sessionID),
		})
	case "remove":
		sessionID, ok := getStringArg(args, "session_id")
		if !ok || strings.TrimSpace(sessionID) == "" {
			return ErrorResult("session_id is required for remove")
		}
		removed, err := t.processes.Remove(strings.TrimSpace(sessionID))
		if err != nil {
			return ErrorResult(err.Error())
		}
		return marshalSilentJSON(map[string]any{
			"status":     "ok",
			"action":     "remove",
			"session_id": strings.TrimSpace(sessionID),
			"removed":    removed,
		})
	default:
		return ErrorResult(fmt.Sprintf("unknown action: %s", action))
	}
}

func marshalSilentJSON(payload any) *ToolResult {
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to encode process payload: %v", err))
	}
	return SilentResult(string(data))
}
