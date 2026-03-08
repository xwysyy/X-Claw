package providers

import (
	"fmt"
	"strings"

	"github.com/xwysyy/X-Claw/pkg/config"
)

func ProtocolForProvider(provider string) string {
	return CanonicalProtocol(provider)
}

func FindModelConfigForFallbackCandidate(cfg *config.Config, candidate FallbackCandidate) *config.ModelConfig {
	if cfg == nil {
		return nil
	}

	alias := strings.TrimSpace(candidate.Model)
	if alias != "" {
		if modelCfg, err := cfg.GetModelConfig(alias); err == nil && modelCfg != nil {
			return modelCfg
		}
	}

	wantProvider := ProtocolForProvider(candidate.Provider)
	wantModel := strings.TrimSpace(candidate.Model)
	for i := range cfg.ModelList {
		protocol, modelID := ExtractProtocol(cfg.ModelList[i].Model)
		if NormalizeProvider(protocol) != NormalizeProvider(wantProvider) {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(modelID), wantModel) {
			return &cfg.ModelList[i]
		}
	}

	return nil
}

func SynthesizeFallbackModelConfig(cfg *config.Config, candidate FallbackCandidate) (*config.ModelConfig, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}

	protocol := ProtocolForProvider(candidate.Provider)
	modelID := strings.TrimSpace(candidate.Model)
	if modelID == "" {
		return nil, fmt.Errorf("fallback model is empty")
	}

	modelCfg := &config.ModelConfig{
		ModelName: modelID,
		Model:     protocol + "/" + modelID,
		Workspace: cfg.WorkspacePath(),
	}

	if providerCfg, ok := providerConfigForProtocol(cfg, protocol); ok {
		modelCfg.APIKey = providerCfg.APIKey
		modelCfg.APIBase = providerCfg.APIBase
		modelCfg.Proxy = providerCfg.Proxy
		modelCfg.RequestTimeout = providerCfg.RequestTimeout
		modelCfg.AuthMethod = providerCfg.AuthMethod
		modelCfg.ConnectMode = providerCfg.ConnectMode
		return modelCfg, nil
	}

	switch protocol {
	case "claude-cli", "codex-cli":
		return modelCfg, nil
	default:
		return nil, fmt.Errorf("unsupported fallback provider %q", candidate.Provider)
	}
}

func providerConfigForProtocol(cfg *config.Config, protocol string) (config.ProviderConfig, bool) {
	if cfg == nil {
		return config.ProviderConfig{}, false
	}
	protocol = CanonicalProtocol(protocol)

	switch protocol {
	case "openai":
		return cfg.Providers.OpenAI.ProviderConfig, true
	case "anthropic":
		return cfg.Providers.Anthropic, true
	case "litellm":
		return cfg.Providers.LiteLLM, true
	case "openrouter":
		return cfg.Providers.OpenRouter, true
	case "groq":
		return cfg.Providers.Groq, true
	case "zhipu":
		return cfg.Providers.Zhipu, true
	case "vllm":
		return cfg.Providers.VLLM, true
	case "gemini":
		return cfg.Providers.Gemini, true
	case "nvidia":
		return cfg.Providers.Nvidia, true
	case "ollama":
		return cfg.Providers.Ollama, true
	case "moonshot":
		return cfg.Providers.Moonshot, true
	case "shengsuanyun":
		return cfg.Providers.ShengSuanYun, true
	case "deepseek":
		return cfg.Providers.DeepSeek, true
	case "cerebras":
		return cfg.Providers.Cerebras, true
	case "volcengine":
		return cfg.Providers.VolcEngine, true
	case "github-copilot":
		return cfg.Providers.GitHubCopilot, true
	case "antigravity":
		return cfg.Providers.Antigravity, true
	case "qwen":
		return cfg.Providers.Qwen, true
	case "mistral":
		return cfg.Providers.Mistral, true
	case "avian":
		return cfg.Providers.Avian, true
	default:
		return config.ProviderConfig{}, false
	}
}
