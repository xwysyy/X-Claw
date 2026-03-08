package agent

import (
	"fmt"
	"strings"

	"github.com/xwysyy/X-Claw/pkg/config"
	"github.com/xwysyy/X-Claw/pkg/providers"
)

func (al *AgentLoop) createProviderForModelAlias(modelAlias string) (providers.LLMProvider, string, error) {
	cfg := al.Config()
	if cfg == nil {
		return nil, "", fmt.Errorf("config is nil")
	}
	modelCfg, err := cfg.GetModelConfig(modelAlias)
	if err != nil {
		return nil, "", err
	}
	cfgCopy := *modelCfg
	if cfgCopy.Workspace == "" {
		cfgCopy.Workspace = cfg.WorkspacePath()
	}
	return providers.CreateProviderFromConfig(&cfgCopy)
}

func (al *AgentLoop) createProviderForFallbackCandidate(candidate providers.FallbackCandidate) (providers.LLMProvider, string, error) {
	cfg := al.Config()
	if cfg == nil {
		return nil, "", fmt.Errorf("config is nil")
	}

	if modelCfg := providers.FindModelConfigForFallbackCandidate(cfg, candidate); modelCfg != nil {
		cfgCopy := *modelCfg
		if cfgCopy.Workspace == "" {
			cfgCopy.Workspace = cfg.WorkspacePath()
		}
		return providers.CreateProviderFromConfig(&cfgCopy)
	}

	modelCfg, err := providers.SynthesizeFallbackModelConfig(cfg, candidate)
	if err != nil {
		return nil, "", err
	}
	return providers.CreateProviderFromConfig(modelCfg)
}

func findFallbackModelConfig(cfg *config.Config, candidate providers.FallbackCandidate) *config.ModelConfig {
	if cfg == nil {
		return nil
	}

	alias := strings.TrimSpace(candidate.Model)
	if alias != "" {
		if modelCfg, err := cfg.GetModelConfig(alias); err == nil && modelCfg != nil {
			return modelCfg
		}
	}

	wantProvider := providerProtocolForFallbackCandidate(candidate.Provider)
	wantModel := strings.TrimSpace(candidate.Model)
	for i := range cfg.ModelList {
		protocol, modelID := providers.ExtractProtocol(cfg.ModelList[i].Model)
		if providers.NormalizeProvider(protocol) != providers.NormalizeProvider(wantProvider) {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(modelID), wantModel) {
			return &cfg.ModelList[i]
		}
	}

	return nil
}

func synthesizeFallbackModelConfig(cfg *config.Config, candidate providers.FallbackCandidate) (*config.ModelConfig, error) {
	protocol := providerProtocolForFallbackCandidate(candidate.Provider)
	modelID := strings.TrimSpace(candidate.Model)
	if modelID == "" {
		return nil, fmt.Errorf("fallback model is empty")
	}

	modelCfg := &config.ModelConfig{
		ModelName: modelID,
		Model:     protocol + "/" + modelID,
		Workspace: cfg.WorkspacePath(),
	}

	copyProviderConfig := func(pc config.ProviderConfig) {
		modelCfg.APIKey = pc.APIKey
		modelCfg.APIBase = pc.APIBase
		modelCfg.Proxy = pc.Proxy
		modelCfg.RequestTimeout = pc.RequestTimeout
		modelCfg.AuthMethod = pc.AuthMethod
		modelCfg.ConnectMode = pc.ConnectMode
	}

	switch protocol {
	case "openai":
		copyProviderConfig(cfg.Providers.OpenAI.ProviderConfig)
	case "anthropic":
		copyProviderConfig(cfg.Providers.Anthropic)
	case "litellm":
		copyProviderConfig(cfg.Providers.LiteLLM)
	case "openrouter":
		copyProviderConfig(cfg.Providers.OpenRouter)
	case "groq":
		copyProviderConfig(cfg.Providers.Groq)
	case "zhipu":
		copyProviderConfig(cfg.Providers.Zhipu)
	case "vllm":
		copyProviderConfig(cfg.Providers.VLLM)
	case "gemini":
		copyProviderConfig(cfg.Providers.Gemini)
	case "nvidia":
		copyProviderConfig(cfg.Providers.Nvidia)
	case "ollama":
		copyProviderConfig(cfg.Providers.Ollama)
	case "moonshot":
		copyProviderConfig(cfg.Providers.Moonshot)
	case "shengsuanyun":
		copyProviderConfig(cfg.Providers.ShengSuanYun)
	case "deepseek":
		copyProviderConfig(cfg.Providers.DeepSeek)
	case "cerebras":
		copyProviderConfig(cfg.Providers.Cerebras)
	case "volcengine":
		copyProviderConfig(cfg.Providers.VolcEngine)
	case "github-copilot":
		copyProviderConfig(cfg.Providers.GitHubCopilot)
	case "antigravity":
		copyProviderConfig(cfg.Providers.Antigravity)
	case "qwen":
		copyProviderConfig(cfg.Providers.Qwen)
	case "mistral":
		copyProviderConfig(cfg.Providers.Mistral)
	case "avian":
		copyProviderConfig(cfg.Providers.Avian)
	case "claude-cli", "codex-cli":
		// Workspace-only providers need no extra config.
	default:
		return nil, fmt.Errorf("unsupported fallback provider %q", candidate.Provider)
	}

	return modelCfg, nil
}

func providerProtocolForFallbackCandidate(provider string) string {
	return providers.CanonicalProtocol(provider)
}
