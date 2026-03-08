package agent

import (
	"context"
	"errors"
	"time"

	"github.com/xwysyy/X-Claw/pkg/bus"
	"github.com/xwysyy/X-Claw/pkg/logger"
)

func (al *AgentLoop) handleReasoning(ctx context.Context, reasoningContent, channelName, channelID string) {
	if reasoningContent == "" || channelName == "" || channelID == "" {
		return
	}
	if ctx.Err() != nil {
		return
	}

	pubCtx, pubCancel := context.WithTimeout(ctx, 5*time.Second)
	defer pubCancel()

	if err := al.bus.PublishOutbound(pubCtx, bus.OutboundMessage{Channel: channelName, ChatID: channelID, Content: reasoningContent}); err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.Is(err, bus.ErrBusClosed) {
			logger.DebugCF("agent", "Reasoning publish skipped (timeout/cancel)", map[string]any{"channel": channelName, "error": err.Error()})
		} else {
			logger.WarnCF("agent", "Failed to publish reasoning (best-effort)", map[string]any{"channel": channelName, "error": err.Error()})
		}
	}
}
