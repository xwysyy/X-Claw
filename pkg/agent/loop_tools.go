package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/xwysyy/X-Claw/pkg/bus"
	"github.com/xwysyy/X-Claw/pkg/logger"
	"github.com/xwysyy/X-Claw/pkg/providers"
	"github.com/xwysyy/X-Claw/pkg/tools"
	"github.com/xwysyy/X-Claw/pkg/utils"
)

func normalizeToolCalls(toolCalls []providers.ToolCall) []providers.ToolCall {
	normalized := make([]providers.ToolCall, 0, len(toolCalls))
	for _, tc := range toolCalls {
		normalized = append(normalized, providers.NormalizeToolCall(tc))
	}
	return normalized
}

func (r *llmIterationRunner) exceedsToolCallBudget(toolCalls []providers.ToolCall) bool {
	if r.maxToolCallsPerRun <= 0 || r.toolCallsUsed+len(toolCalls) <= r.maxToolCallsPerRun {
		return false
	}
	r.finalContent = fmt.Sprintf(
		"RESOURCE_BUDGET_EXCEEDED: tool call budget exceeded (%d). Please narrow the request or reduce the number of tools used.",
		r.maxToolCallsPerRun,
	)
	logger.WarnCF("agent", "Resource budget exceeded (tool calls)", map[string]any{
		"agent_id":           r.agent.ID,
		"iteration":          r.iteration,
		"tool_calls_used":    r.toolCallsUsed,
		"tool_calls_pending": len(toolCalls),
		"tool_calls_budget":  r.maxToolCallsPerRun,
		"session_key":        r.opts.SessionKey,
	})
	return true
}

func (r *llmIterationRunner) updateWorkingStateHint(content string) {
	reasoning := strings.TrimSpace(content)
	if reasoning == "" || r.opts.WorkingState == nil {
		return
	}
	hint := reasoning
	if len(hint) > 200 {
		hint = hint[:200] + "..."
	}
	r.opts.WorkingState.SetNextAction(hint)
}

func (r *llmIterationRunner) logRequestedToolCalls(toolCalls []providers.ToolCall) {
	toolNames := make([]string, 0, len(toolCalls))
	for _, tc := range toolCalls {
		toolNames = append(toolNames, tc.Name)
	}
	logger.InfoCF("agent", "LLM requested tool calls",
		map[string]any{
			"agent_id":  r.agent.ID,
			"tools":     toolNames,
			"count":     len(toolCalls),
			"iteration": r.iteration,
		})
}

func (r *llmIterationRunner) handleToolLoop(response *providers.LLMResponse, toolCalls []providers.ToolCall) bool {
	loopingTool := detectToolCallLoop(r.recentToolCalls, toolCalls, 3)
	if loopingTool == "" {
		return false
	}
	logger.WarnCF("agent", "Tool call loop detected",
		map[string]any{
			"agent_id":  r.agent.ID,
			"tool":      loopingTool,
			"iteration": r.iteration,
		})

	loopAssistantMsg := providers.Message{Role: "assistant", Content: response.Content}
	for _, tc := range toolCalls {
		argumentsJSON, _ := json.Marshal(tc.Arguments)
		loopAssistantMsg.ToolCalls = append(loopAssistantMsg.ToolCalls, providers.ToolCall{
			ID:   tc.ID,
			Type: "function",
			Name: tc.Name,
			Function: &providers.FunctionCall{
				Name:      tc.Name,
				Arguments: string(argumentsJSON),
			},
		})
	}
	r.messages = append(r.messages, loopAssistantMsg)

	loopNotice := fmt.Sprintf("Loop detected: '%s' called with same arguments 3+ times. Try a different approach, use a different tool, or explain why you are stuck.", loopingTool)
	for _, tc := range toolCalls {
		r.messages = append(r.messages, providers.Message{Role: "tool", Content: loopNotice, ToolCallID: tc.ID})
	}
	return true
}

func toolResultFingerprint(result *tools.ToolResult) string {
	if result == nil {
		return "<nil>"
	}
	errText := ""
	if result.Err != nil {
		errText = utils.TruncateHeadTail(strings.TrimSpace(result.Err.Error()), 120, 40)
	}
	return fmt.Sprintf(
		"is_error=%t|async=%t|llm=%s|user=%s|err=%s|media=%s",
		result.IsError,
		result.Async,
		utils.TruncateHeadTail(strings.TrimSpace(result.ForLLM), 160, 50),
		utils.TruncateHeadTail(strings.TrimSpace(result.ForUser), 120, 40),
		errText,
		strings.Join(result.Media, ","),
	)
}

func (r *llmIterationRunner) recordRecentToolCalls(toolExecutions []tools.ToolCallExecution) {
	for _, execution := range toolExecutions {
		argsJSON, _ := json.Marshal(execution.ToolCall.Arguments)
		r.recentToolCalls = append(r.recentToolCalls, toolCallSignature{
			Name:              execution.ToolCall.Name,
			Args:              string(argsJSON),
			ResultFingerprint: toolResultFingerprint(execution.Result),
		})
	}
	const maxRecentToolCalls = 24
	if len(r.recentToolCalls) > maxRecentToolCalls {
		r.recentToolCalls = append([]toolCallSignature(nil), r.recentToolCalls[len(r.recentToolCalls)-maxRecentToolCalls:]...)
	}
}

func (r *llmIterationRunner) appendAssistantToolCallMessage(response *providers.LLMResponse, toolCalls []providers.ToolCall) {
	assistantMsg := providers.Message{Role: "assistant", Content: response.Content, ReasoningContent: response.ReasoningContent}
	for _, tc := range toolCalls {
		argumentsJSON, _ := json.Marshal(tc.Arguments)
		extraContent := tc.ExtraContent
		thoughtSignature := ""
		if tc.Function != nil {
			thoughtSignature = tc.Function.ThoughtSignature
		}
		assistantMsg.ToolCalls = append(assistantMsg.ToolCalls, providers.ToolCall{
			ID:   tc.ID,
			Type: "function",
			Name: tc.Name,
			Function: &providers.FunctionCall{
				Name:             tc.Name,
				Arguments:        string(argumentsJSON),
				ThoughtSignature: thoughtSignature,
			},
			ExtraContent:     extraContent,
			ThoughtSignature: thoughtSignature,
		})
	}
	r.messages = append(r.messages, assistantMsg)
	addSessionFullMessage(r.agent.Sessions, r.opts.SessionKey, assistantMsg)
}

func (r *llmIterationRunner) executeToolCalls(toolCalls []providers.ToolCall) []tools.ToolCallExecution {
	cfg := r.loop.Config()
	parallelCfg := tools.ToolCallParallelConfig{Enabled: cfg != nil && cfg.Orchestration.ToolCallsParallelEnabled}
	if cfg != nil {
		parallelCfg.MaxConcurrency = cfg.Orchestration.MaxToolCallConcurrency
		parallelCfg.Mode = cfg.Orchestration.ParallelToolsMode
		parallelCfg.ToolPolicyOverrides = cfg.Orchestration.ToolParallelOverrides
	}

	traceOpts := tools.ToolTraceOptions{}
	if cfg != nil {
		traceOpts.Enabled = cfg.Tools.Trace.Enabled
		traceOpts.Dir = cfg.Tools.Trace.Dir
		traceOpts.WritePerCallFiles = cfg.Tools.Trace.WritePerCallFiles
		traceOpts.MaxArgPreviewChars = cfg.Tools.Trace.MaxArgPreviewChars
		traceOpts.MaxResultPreviewChars = cfg.Tools.Trace.MaxResultPreviewChars
	}

	errorTemplateOpts := tools.ToolErrorTemplateOptions{}
	if cfg != nil {
		errorTemplateOpts.Enabled = cfg.Tools.ErrorTemplate.Enabled
		errorTemplateOpts.IncludeSchema = cfg.Tools.ErrorTemplate.IncludeSchema
		errorTemplateOpts.IncludeAvailableTools = true
	}

	toolExecutions := tools.ExecuteToolCalls(r.ctx, r.agent.Tools, toolCalls, tools.ToolCallExecutionOptions{
		Channel:        r.opts.Channel,
		ChatID:         r.opts.ChatID,
		SenderID:       r.opts.SenderID,
		Workspace:      r.agent.Workspace,
		SessionKey:     r.opts.SessionKey,
		RunID:          r.runID(),
		IsResume:       r.opts.Resume,
		Iteration:      r.iteration,
		LogScope:       "agent",
		Parallel:       parallelCfg,
		Trace:          traceOpts,
		MaxResultChars: r.maxToolResultChars,
		ErrorTemplate:  errorTemplateOpts,
		Hooks:          tools.BuildDefaultToolHooks(cfg),
		AsyncCallbackForCall: func(call providers.ToolCall) tools.AsyncCallback {
			return func(callbackCtx context.Context, result *tools.ToolResult) {
				if result == nil {
					return
				}
				if !result.Silent && result.ForUser != "" {
					logger.InfoCF("agent", "Async tool completed, agent will handle notification",
						map[string]any{"tool": call.Name, "content_len": len(result.ForUser)})
				}
			}
		},
	})
	r.toolCallsUsed += len(toolExecutions)
	if r.trace != nil {
		r.trace.recordToolBatch(r.iteration, toolExecutions)
	}
	return toolExecutions
}

func (r *llmIterationRunner) applyToolExecutionResults(toolExecutions []tools.ToolCallExecution) {
	for _, executed := range toolExecutions {
		toolResult := executed.Result
		tc := executed.ToolCall

		if ws := r.opts.WorkingState; ws != nil {
			ws.RecordToolCall(tc.Name, toolResult.IsError)
			outcome := toolResult.ForLLM
			if len(outcome) > 120 {
				outcome = outcome[:120] + "..."
			}
			if toolResult.IsError {
				outcome = "[error] " + outcome
			}
			ws.AddCompletedStep(tc.Name, outcome, tc.Name)
		}

		if !toolResult.Silent && toolResult.ForUser != "" && r.opts.SendResponse {
			r.loop.bus.PublishOutbound(r.ctx, bus.OutboundMessage{Channel: r.opts.Channel, ChatID: r.opts.ChatID, Content: toolResult.ForUser})
			logger.DebugCF("agent", "Sent tool result to user",
				map[string]any{"tool": tc.Name, "content_len": len(toolResult.ForUser)})
		}

		if len(toolResult.Media) > 0 && r.opts.SendResponse {
			parts := make([]bus.MediaPart, 0, len(toolResult.Media))
			for _, ref := range toolResult.Media {
				part := bus.MediaPart{Ref: ref}
				if r.loop.mediaResolver != nil {
					if _, meta, err := r.loop.mediaResolver.ResolveWithMeta(ref); err == nil {
						part.Filename = strings.TrimSpace(meta.Filename)
						part.ContentType = strings.TrimSpace(meta.ContentType)
						part.Type = inferMediaType(part.Filename, part.ContentType)
					}
				}
				parts = append(parts, part)
			}
			r.loop.bus.PublishOutboundMedia(r.ctx, bus.OutboundMediaMessage{Channel: r.opts.Channel, ChatID: r.opts.ChatID, Parts: parts})
		}

		contentForLLM := toolResult.ForLLM
		if contentForLLM == "" && toolResult.Err != nil {
			contentForLLM = toolResult.Err.Error()
		}
		toolResultMsg := providers.Message{Role: "tool", Content: contentForLLM, ToolCallID: tc.ID}
		r.messages = append(r.messages, toolResultMsg)
		addSessionFullMessage(r.agent.Sessions, r.opts.SessionKey, toolResultMsg)
	}
}
