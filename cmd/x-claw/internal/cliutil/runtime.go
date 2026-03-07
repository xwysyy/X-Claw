package cliutil

import (
	"fmt"

	"github.com/xwysyy/X-Claw/cmd/x-claw/internal"
	"github.com/xwysyy/X-Claw/pkg/agent"
	"github.com/xwysyy/X-Claw/pkg/bus"
	"github.com/xwysyy/X-Claw/pkg/config"
	"github.com/xwysyy/X-Claw/pkg/providers"
)

var (
	loadRuntimeConfig     = internal.LoadConfig
	createRuntimeProvider = providers.CreateProvider
	newRuntimeMessageBus  = bus.NewMessageBus
	newRuntimeAgentLoop   = agent.NewAgentLoop
)

type RuntimeBootstrap struct {
	Config     *config.Config
	Provider   providers.LLMProvider
	ModelID    string
	MessageBus *bus.MessageBus
	AgentLoop  *agent.AgentLoop
}

func BootstrapRuntime(modelOverride string) (*RuntimeBootstrap, error) {
	cfg, err := loadRuntimeConfig()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	if modelOverride != "" {
		cfg.Agents.Defaults.ModelName = modelOverride
	}

	provider, modelID, err := createRuntimeProvider(cfg)
	if err != nil {
		return nil, fmt.Errorf("create provider: %w", err)
	}
	if modelID != "" {
		cfg.Agents.Defaults.ModelName = modelID
	}

	msgBus := newRuntimeMessageBus()
	agentLoop := newRuntimeAgentLoop(cfg, msgBus, provider)
	return &RuntimeBootstrap{
		Config:     cfg,
		Provider:   provider,
		ModelID:    modelID,
		MessageBus: msgBus,
		AgentLoop:  agentLoop,
	}, nil
}

func BootstrapAgentRuntime(modelOverride string) (*RuntimeBootstrap, error) {
	return BootstrapRuntime(modelOverride)
}
