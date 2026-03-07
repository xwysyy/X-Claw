package cliutil

import (
	"context"
	"errors"
	"testing"

	"github.com/xwysyy/X-Claw/internal/core/provider/protocoltypes"
	"github.com/xwysyy/X-Claw/pkg/agent"
	"github.com/xwysyy/X-Claw/pkg/bus"
	"github.com/xwysyy/X-Claw/pkg/config"
	"github.com/xwysyy/X-Claw/pkg/providers"
)

type fakeProvider struct{}

func (fakeProvider) Chat(context.Context, []protocoltypes.Message, []protocoltypes.ToolDefinition, string, map[string]any) (*protocoltypes.LLMResponse, error) {
	return nil, errors.New("not implemented")
}

func (fakeProvider) GetDefaultModel() string { return "fake-default" }

func TestBootstrapRuntime_AppliesOverrideAndResolvedModelID(t *testing.T) {
	origLoad := loadRuntimeConfig
	origCreate := createRuntimeProvider
	origNewBus := newRuntimeMessageBus
	origNewLoop := newRuntimeAgentLoop
	t.Cleanup(func() {
		loadRuntimeConfig = origLoad
		createRuntimeProvider = origCreate
		newRuntimeMessageBus = origNewBus
		newRuntimeAgentLoop = origNewLoop
	})

	cfg := &config.Config{}
	cfg.Agents.Defaults.ModelName = "original"

	var gotCreateModel string
	var gotLoopCfg *config.Config
	var gotLoopProvider providers.LLMProvider
	var gotLoopBus *bus.MessageBus
	stubBus := bus.NewMessageBus()
	t.Cleanup(stubBus.Close)
	stubLoop := &agent.AgentLoop{}
	stubProvider := fakeProvider{}

	loadRuntimeConfig = func() (*config.Config, error) {
		return cfg, nil
	}
	createRuntimeProvider = func(got *config.Config) (providers.LLMProvider, string, error) {
		gotCreateModel = got.Agents.Defaults.ModelName
		return stubProvider, "resolved-model", nil
	}
	newRuntimeMessageBus = func() *bus.MessageBus {
		return stubBus
	}
	newRuntimeAgentLoop = func(gotCfg *config.Config, gotBus *bus.MessageBus, gotProvider providers.LLMProvider) *agent.AgentLoop {
		gotLoopCfg = gotCfg
		gotLoopBus = gotBus
		gotLoopProvider = gotProvider
		return stubLoop
	}

	runtime, err := BootstrapRuntime("override-model")
	if err != nil {
		t.Fatalf("BootstrapRuntime error = %v", err)
	}
	if gotCreateModel != "override-model" {
		t.Fatalf("provider saw model %q, want override-model", gotCreateModel)
	}
	if runtime.Config != cfg {
		t.Fatalf("runtime.Config = %p, want original cfg %p", runtime.Config, cfg)
	}
	if cfg.Agents.Defaults.ModelName != "resolved-model" {
		t.Fatalf("cfg model = %q, want resolved-model", cfg.Agents.Defaults.ModelName)
	}
	if runtime.Provider != stubProvider {
		t.Fatalf("runtime.Provider mismatch")
	}
	if runtime.ModelID != "resolved-model" {
		t.Fatalf("runtime.ModelID = %q, want resolved-model", runtime.ModelID)
	}
	if runtime.MessageBus != stubBus {
		t.Fatalf("runtime.MessageBus mismatch")
	}
	if runtime.AgentLoop != stubLoop {
		t.Fatalf("runtime.AgentLoop mismatch")
	}
	if gotLoopCfg != cfg || gotLoopBus != stubBus || gotLoopProvider != stubProvider {
		t.Fatalf("agent loop inputs not wired correctly")
	}
}

func TestBootstrapAgentRuntime_UsesSharedBootstrapPath(t *testing.T) {
	origLoad := loadRuntimeConfig
	origCreate := createRuntimeProvider
	origNewBus := newRuntimeMessageBus
	origNewLoop := newRuntimeAgentLoop
	t.Cleanup(func() {
		loadRuntimeConfig = origLoad
		createRuntimeProvider = origCreate
		newRuntimeMessageBus = origNewBus
		newRuntimeAgentLoop = origNewLoop
	})

	cfg := &config.Config{}
	cfg.Agents.Defaults.ModelName = "original"
	stubBus := bus.NewMessageBus()
	t.Cleanup(stubBus.Close)
	stubLoop := &agent.AgentLoop{}
	stubProvider := fakeProvider{}

	loadRuntimeConfig = func() (*config.Config, error) { return cfg, nil }
	createRuntimeProvider = func(got *config.Config) (providers.LLMProvider, string, error) {
		if got.Agents.Defaults.ModelName != "cli-model" {
			t.Fatalf("provider saw model %q, want cli-model", got.Agents.Defaults.ModelName)
		}
		return stubProvider, "", nil
	}
	newRuntimeMessageBus = func() *bus.MessageBus { return stubBus }
	newRuntimeAgentLoop = func(*config.Config, *bus.MessageBus, providers.LLMProvider) *agent.AgentLoop {
		return stubLoop
	}

	runtime, err := BootstrapAgentRuntime("cli-model")
	if err != nil {
		t.Fatalf("BootstrapAgentRuntime error = %v", err)
	}
	if runtime.Config != cfg || runtime.Provider != stubProvider || runtime.MessageBus != stubBus || runtime.AgentLoop != stubLoop {
		t.Fatalf("BootstrapAgentRuntime did not use shared bootstrap path")
	}
}
