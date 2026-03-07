package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/xwysyy/X-Claw/pkg/bus"
	"github.com/xwysyy/X-Claw/pkg/config"
	"github.com/xwysyy/X-Claw/pkg/constants"
	"github.com/xwysyy/X-Claw/pkg/logger"
	"github.com/xwysyy/X-Claw/pkg/providers"
	"github.com/xwysyy/X-Claw/pkg/utils"
)

func (al *AgentLoop) recordLastExternalChannel(opts processOptions) {
	if opts.Channel == "" || opts.ChatID == "" || constants.IsInternalChannel(opts.Channel) {
		return
	}
	channelKey := fmt.Sprintf("%s:%s", opts.Channel, opts.ChatID)
	if err := al.RecordLastChannel(channelKey); err != nil {
		logger.WarnCF(
			"agent",
			"Failed to record last channel",
			map[string]any{"error": err.Error()},
		)
	}
	key := utils.CanonicalSessionKey(opts.SessionKey)
	if key == "" || strings.HasPrefix(key, "cron-") || strings.EqualFold(strings.TrimSpace(opts.SenderID), "cron") {
		return
	}
	if err := al.RecordLastSessionKey(key); err != nil {
		logger.WarnCF(
			"agent",
			"Failed to record last session key",
			map[string]any{"error": err.Error(), "session_key": key},
		)
	}
}

func newRunTraceForMessages(
	agent *AgentInstance,
	opts processOptions,
	cfg *config.Config,
	messages []providers.Message,
	modelForRun string,
) *runTraceWriter {
	runTraceEnabled := cfg != nil && cfg.Tools.Trace.Enabled
	runTrace := newRunTraceWriter(agent.Workspace, runTraceEnabled, opts, agent.ID, modelForRun)
	if runTrace == nil {
		return nil
	}
	if opts.Resume {
		runTrace.recordResume(opts.UserMessage, len(messages), len(agent.Tools.List()))
	} else {
		runTrace.recordStart(opts.UserMessage, len(messages), len(agent.Tools.List()))
	}
	return runTrace
}

func (al *AgentLoop) finalizeRun(
	ctx context.Context,
	agent *AgentInstance,
	opts processOptions,
	cfg *config.Config,
	runTrace *runTraceWriter,
	finalContent string,
	iteration int,
) string {
	if finalContent == "" {
		finalContent = opts.DefaultResponse
	}

	addSessionMessage(agent.Sessions, opts.SessionKey, "assistant", finalContent)
	saveSessionBestEffort(agent.Sessions, opts.SessionKey, "Failed to persist assistant message (best-effort)", nil)

	if opts.EnableSummary {
		al.maybeSummarize(agent, opts.SessionKey, opts.Channel, opts.ChatID)
	}
	if opts.SendResponse {
		al.bus.PublishOutbound(ctx, bus.OutboundMessage{
			Channel: opts.Channel,
			ChatID:  opts.ChatID,
			Content: finalContent,
		})
	}

	responsePreview := utils.Truncate(finalContent, 120)
	logger.InfoCF("agent", fmt.Sprintf("Response: %s", responsePreview),
		map[string]any{
			"agent_id":     agent.ID,
			"session_key":  opts.SessionKey,
			"iterations":   iteration,
			"final_length": len(finalContent),
		})

	if runTrace != nil {
		runTrace.recordEnd(iteration, finalContent)
	}
	if cfg != nil && cfg.Notify.OnTaskComplete && constants.IsInternalChannel(opts.Channel) {
		al.notifyLastActiveOnInternalRun(ctx, agent, opts, finalContent)
	}
	return finalContent
}

func (al *AgentLoop) notifyLastActiveOnInternalRun(
	ctx context.Context,
	agent *AgentInstance,
	opts processOptions,
	finalContent string,
) {
	trimmedResult := strings.TrimSpace(finalContent)
	if trimmedResult == "" ||
		strings.EqualFold(trimmedResult, "NO_UPDATE") ||
		strings.EqualFold(trimmedResult, "HEARTBEAT_OK") {
		return
	}

	targetCh, targetChat := al.LastActive()
	if strings.TrimSpace(targetCh) == "" || strings.TrimSpace(targetChat) == "" || constants.IsInternalChannel(targetCh) {
		return
	}

	notifyText := fmt.Sprintf(
		"✅ Task complete\n\nTask:\n%s\n\nResult:\n%s",
		utils.Truncate(strings.TrimSpace(opts.UserMessage), 240),
		utils.Truncate(strings.TrimSpace(finalContent), 1200),
	)
	if tool, ok := agent.Tools.Get("message"); ok && tool != nil {
		result := tool.Execute(ctx, map[string]any{
			"content": notifyText,
			"channel": targetCh,
			"chat_id": targetChat,
		})
		if result == nil {
			logger.WarnCF("agent", "message tool returned nil completion notification result", map[string]any{"channel": targetCh, "chat_id": targetChat})
			return
		}
		if result.IsError {
			logger.WarnCF("agent", "message tool failed to send completion notification", map[string]any{"channel": targetCh, "chat_id": targetChat, "error": result.ForLLM})
		}
		return
	}
	if al.bus != nil {
		if err := al.bus.PublishOutbound(ctx, bus.OutboundMessage{
			Channel: targetCh,
			ChatID:  targetChat,
			Content: notifyText,
		}); err != nil {
			logger.DebugCF("agent", "failed to publish completion notification", map[string]any{"channel": targetCh, "chat_id": targetChat, "error": err.Error()})
		}
	}
}
