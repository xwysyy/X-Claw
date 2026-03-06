package providers

import (
	"fmt"
	"strings"

	"github.com/xwysyy/X-Claw/pkg/config"
)

func resolveProviderSelection(cfg *config.Config) (providerSelection, error) {
	model := cfg.Agents.Defaults.GetModelName()
	sel := providerSelection{
		providerType: providerTypeHTTPCompat,
		model:        model,
	}

	resolved, done, err := resolveExplicitProviderSelection(cfg, sel)
	if err != nil {
		return providerSelection{}, err
	}
	if done {
		return validateProviderSelection(resolved)
	}

	resolved, err = resolveInferredProviderSelection(cfg, resolved)
	if err != nil {
		return providerSelection{}, err
	}
	return validateProviderSelection(resolved)
}

func resolveExplicitProviderSelection(cfg *config.Config, sel providerSelection) (providerSelection, bool, error) {
	providerName := strings.ToLower(cfg.Agents.Defaults.Provider)
	model := sel.model
	if providerName == "" {
		return sel, false, nil
	}

	switch providerName {
	case "groq":
		return applyHTTPProviderSelection(sel, cfg.Providers.Groq, "https://api.groq.com/openai/v1")
	case "openai", "gpt":
		return resolveOpenAIProviderSelection(sel, cfg)
	case "anthropic", "claude":
		return resolveAnthropicProviderSelection(sel, cfg)
	case "openrouter":
		return applyHTTPProviderSelection(sel, cfg.Providers.OpenRouter, "https://openrouter.ai/api/v1")
	case "litellm":
		return resolveLiteLLMProviderSelection(sel, cfg)
	case "zhipu", "glm":
		return applyHTTPProviderSelection(sel, cfg.Providers.Zhipu, "https://open.bigmodel.cn/api/paas/v4")
	case "gemini", "google":
		return applyHTTPProviderSelection(sel, cfg.Providers.Gemini, "https://generativelanguage.googleapis.com/v1beta")
	case "vllm":
		return applyHTTPProviderSelection(sel, cfg.Providers.VLLM, "")
	case "shengsuanyun":
		return applyHTTPProviderSelection(sel, cfg.Providers.ShengSuanYun, "https://router.shengsuanyun.com/api/v1")
	case "nvidia":
		return applyHTTPProviderSelection(sel, cfg.Providers.Nvidia, "https://integrate.api.nvidia.com/v1")
	case "claude-cli", "claude-code", "claudecode":
		return resolveWorkspaceCLIProviderSelection(sel, cfg, providerTypeClaudeCLI), true, nil
	case "codex-cli", "codex-code":
		return resolveWorkspaceCLIProviderSelection(sel, cfg, providerTypeCodexCLI), true, nil
	case "deepseek":
		return resolveDeepSeekProviderSelection(sel, cfg, model)
	case "avian":
		return applyHTTPProviderSelection(sel, cfg.Providers.Avian, "https://api.avian.io/v1")
	case "mistral":
		return applyHTTPProviderSelection(sel, cfg.Providers.Mistral, "https://api.mistral.ai/v1")
	case "github_copilot", "copilot":
		return resolveGitHubCopilotProviderSelection(sel, cfg), true, nil
	default:
		return sel, false, nil
	}
}

func resolveInferredProviderSelection(cfg *config.Config, sel providerSelection) (providerSelection, error) {
	if sel.apiKey != "" || sel.apiBase != "" || sel.providerType != providerTypeHTTPCompat {
		return sel, nil
	}

	model := sel.model
	lowerModel := strings.ToLower(model)
	switch {
	case (strings.Contains(lowerModel, "kimi") || strings.Contains(lowerModel, "moonshot") || strings.HasPrefix(model, "moonshot/")) && cfg.Providers.Moonshot.APIKey.Present():
		return withAppliedProviderConfig(sel, cfg.Providers.Moonshot, "https://api.moonshot.cn/v1")
	case strings.HasPrefix(model, "openrouter/") ||
		strings.HasPrefix(model, "anthropic/") ||
		strings.HasPrefix(model, "openai/") ||
		strings.HasPrefix(model, "meta-llama/") ||
		strings.HasPrefix(model, "deepseek/") ||
		strings.HasPrefix(model, "google/"):
		return withAppliedProviderConfig(sel, cfg.Providers.OpenRouter, "https://openrouter.ai/api/v1")
	case (strings.Contains(lowerModel, "claude") || strings.HasPrefix(model, "anthropic/")) &&
		(cfg.Providers.Anthropic.APIKey.Present() || cfg.Providers.Anthropic.AuthMethod != ""):
		resolved, _, err := resolveAnthropicProviderSelection(sel, cfg)
		return resolved, err
	case (strings.Contains(lowerModel, "gpt") || strings.HasPrefix(model, "openai/")) &&
		(cfg.Providers.OpenAI.APIKey.Present() || cfg.Providers.OpenAI.AuthMethod != ""):
		resolved, _, err := resolveOpenAIProviderSelection(sel, cfg)
		return resolved, err
	case (strings.Contains(lowerModel, "gemini") || strings.HasPrefix(model, "google/")) && cfg.Providers.Gemini.APIKey.Present():
		return withAppliedProviderConfig(sel, cfg.Providers.Gemini, "https://generativelanguage.googleapis.com/v1beta")
	case (strings.Contains(lowerModel, "glm") || strings.Contains(lowerModel, "zhipu") || strings.Contains(lowerModel, "zai")) && cfg.Providers.Zhipu.APIKey.Present():
		return withAppliedProviderConfig(sel, cfg.Providers.Zhipu, "https://open.bigmodel.cn/api/paas/v4")
	case (strings.Contains(lowerModel, "groq") || strings.HasPrefix(model, "groq/")) && cfg.Providers.Groq.APIKey.Present():
		return withAppliedProviderConfig(sel, cfg.Providers.Groq, "https://api.groq.com/openai/v1")
	case (strings.Contains(lowerModel, "nvidia") || strings.HasPrefix(model, "nvidia/")) && cfg.Providers.Nvidia.APIKey.Present():
		return withAppliedProviderConfig(sel, cfg.Providers.Nvidia, "https://integrate.api.nvidia.com/v1")
	case (strings.Contains(lowerModel, "ollama") || strings.HasPrefix(model, "ollama/")) && cfg.Providers.Ollama.APIKey.Present():
		return withAppliedProviderConfig(sel, cfg.Providers.Ollama, "http://localhost:11434/v1")
	case (strings.Contains(lowerModel, "mistral") || strings.HasPrefix(model, "mistral/")) && cfg.Providers.Mistral.APIKey.Present():
		return withAppliedProviderConfig(sel, cfg.Providers.Mistral, "https://api.mistral.ai/v1")
	case strings.HasPrefix(model, "avian/") && cfg.Providers.Avian.APIKey.Present():
		return withAppliedProviderConfig(sel, cfg.Providers.Avian, "https://api.avian.io/v1")
	case cfg.Providers.VLLM.APIBase != "":
		return withAppliedProviderConfig(sel, cfg.Providers.VLLM, "")
	default:
		if cfg.Providers.OpenRouter.APIKey.Present() {
			return withAppliedProviderConfig(sel, cfg.Providers.OpenRouter, "https://openrouter.ai/api/v1")
		}
		return providerSelection{}, fmt.Errorf("no API key configured for model: %s", model)
	}
}

func resolveOpenAIProviderSelection(sel providerSelection, cfg *config.Config) (providerSelection, bool, error) {
	if !cfg.Providers.OpenAI.APIKey.Present() && cfg.Providers.OpenAI.AuthMethod == "" {
		return sel, false, nil
	}
	sel.enableWebSearch = cfg.Providers.OpenAI.WebSearch
	switch cfg.Providers.OpenAI.AuthMethod {
	case "codex-cli":
		sel.providerType = providerTypeCodexCLIToken
		return sel, true, nil
	case "oauth", "token":
		sel.providerType = providerTypeCodexAuth
		return sel, true, nil
	default:
		resolved, err := withAppliedProviderConfig(sel, cfg.Providers.OpenAI.ProviderConfig, "https://api.openai.com/v1")
		return resolved, false, err
	}
}

func resolveAnthropicProviderSelection(sel providerSelection, cfg *config.Config) (providerSelection, bool, error) {
	if !cfg.Providers.Anthropic.APIKey.Present() && cfg.Providers.Anthropic.AuthMethod == "" {
		return sel, false, nil
	}
	switch cfg.Providers.Anthropic.AuthMethod {
	case "oauth", "token":
		sel.apiBase = cfg.Providers.Anthropic.APIBase
		if sel.apiBase == "" {
			sel.apiBase = defaultAnthropicAPIBase
		}
		sel.providerType = providerTypeClaudeAuth
		return sel, true, nil
	default:
		resolved, err := withAppliedProviderConfig(sel, cfg.Providers.Anthropic, defaultAnthropicAPIBase)
		return resolved, false, err
	}
}

func resolveLiteLLMProviderSelection(sel providerSelection, cfg *config.Config) (providerSelection, bool, error) {
	if !cfg.Providers.LiteLLM.APIKey.Present() && cfg.Providers.LiteLLM.APIBase == "" {
		return sel, false, nil
	}
	if cfg.Providers.LiteLLM.APIKey.Present() {
		v, err := cfg.Providers.LiteLLM.APIKey.Resolve("")
		if err != nil {
			return providerSelection{}, false, err
		}
		sel.apiKey = v
	}
	sel.apiBase = cfg.Providers.LiteLLM.APIBase
	sel.proxy = cfg.Providers.LiteLLM.Proxy
	if sel.apiBase == "" {
		sel.apiBase = "http://localhost:4000/v1"
	}
	return sel, false, nil
}

func resolveWorkspaceCLIProviderSelection(sel providerSelection, cfg *config.Config, providerType providerType) providerSelection {
	workspace := cfg.WorkspacePath()
	if workspace == "" {
		workspace = "."
	}
	sel.providerType = providerType
	sel.workspace = workspace
	return sel
}

func resolveDeepSeekProviderSelection(sel providerSelection, cfg *config.Config, model string) (providerSelection, bool, error) {
	if !cfg.Providers.DeepSeek.APIKey.Present() {
		return sel, false, nil
	}
	resolved, err := withAppliedProviderConfig(sel, cfg.Providers.DeepSeek, "https://api.deepseek.com/v1")
	if err != nil {
		return providerSelection{}, false, err
	}
	if model != "deepseek-chat" && model != "deepseek-reasoner" {
		resolved.model = "deepseek-chat"
	}
	return resolved, false, nil
}

func resolveGitHubCopilotProviderSelection(sel providerSelection, cfg *config.Config) providerSelection {
	sel.providerType = providerTypeGitHubCopilot
	if cfg.Providers.GitHubCopilot.APIBase != "" {
		sel.apiBase = cfg.Providers.GitHubCopilot.APIBase
	} else {
		sel.apiBase = "localhost:4321"
	}
	sel.connectMode = cfg.Providers.GitHubCopilot.ConnectMode
	return sel
}

func applyHTTPProviderSelection(sel providerSelection, pc config.ProviderConfig, defaultBase string) (providerSelection, bool, error) {
	if !pc.APIKey.Present() && strings.TrimSpace(pc.APIBase) == "" {
		return sel, false, nil
	}
	resolved, err := withAppliedProviderConfig(sel, pc, defaultBase)
	return resolved, false, err
}

func withAppliedProviderConfig(sel providerSelection, pc config.ProviderConfig, defaultBase string) (providerSelection, error) {
	if err := applyProviderConfig(&sel, pc, defaultBase); err != nil {
		return providerSelection{}, err
	}
	return sel, nil
}

func validateProviderSelection(sel providerSelection) (providerSelection, error) {
	if sel.providerType == providerTypeHTTPCompat {
		if sel.apiKey == "" && !strings.HasPrefix(sel.model, "bedrock/") {
			return providerSelection{}, fmt.Errorf("no API key configured for provider (model: %s)", sel.model)
		}
		if sel.apiBase == "" {
			return providerSelection{}, fmt.Errorf("no API base configured for provider (model: %s)", sel.model)
		}
	}
	return sel, nil
}
