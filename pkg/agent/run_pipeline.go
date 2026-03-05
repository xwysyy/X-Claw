package agent

import (
	"context"

	"github.com/xwysyy/X-Claw/pkg/bus"
)

func (al *AgentLoop) processMessage(ctx context.Context, msg bus.InboundMessage) (string, error) {
	return al.processMessageImpl(ctx, msg)
}

func (al *AgentLoop) processSystemMessage(ctx context.Context, msg bus.InboundMessage) (string, error) {
	return al.processSystemMessageImpl(ctx, msg)
}

func (al *AgentLoop) runAgentLoop(
	ctx context.Context,
	agent *AgentInstance,
	opts processOptions,
) (string, error) {
	return al.runAgentLoopImpl(ctx, agent, opts)
}
