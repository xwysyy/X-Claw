// X-Claw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 X-Claw contributors

package providers

import (
	"fmt"
	"strings"

	"github.com/xwysyy/X-Claw/pkg/config"
)

// ExtractProtocol extracts the protocol prefix and model identifier from a model string.
// If no prefix is specified, it defaults to "openai".
// Examples:
//   - "openai/gpt-4o" -> ("openai", "gpt-4o")
//   - "anthropic/claude-sonnet-4.6" -> ("anthropic", "claude-sonnet-4.6")
//   - "gpt-4o" -> ("openai", "gpt-4o")  // default protocol
func ExtractProtocol(model string) (protocol, modelID string) {
	model = strings.TrimSpace(model)
	protocol, modelID, found := strings.Cut(model, "/")
	if !found {
		return "openai", model
	}
	return CanonicalProtocol(protocol), strings.TrimSpace(modelID)
}

// CreateProviderFromConfig creates a provider based on the ModelConfig.
// It uses the protocol prefix in the Model field to determine which provider to create.
// Supported protocols: openai, litellm, anthropic, antigravity, claude-cli, codex-cli, github-copilot
// Returns the provider, the model ID (without protocol prefix), and any error.
func CreateProviderFromConfig(cfg *config.ModelConfig) (LLMProvider, string, error) {
	if cfg == nil {
		return nil, "", fmt.Errorf("config is nil")
	}

	if cfg.Model == "" {
		return nil, "", fmt.Errorf("model is required")
	}

	protocol, modelID := ExtractProtocol(cfg.Model)

	apiKey := ""
	if cfg.APIKey.Present() {
		v, err := cfg.APIKey.Resolve("")
		if err != nil {
			return nil, "", fmt.Errorf("resolve api_key for model %q: %w", strings.TrimSpace(cfg.ModelName), err)
		}
		apiKey = v
	}

	switch protocol {
	case "openai":
		apiBase := resolveHTTPProviderAPIBase(cfg, protocol)
		// OpenAI with OAuth/token auth (Codex-style)
		if cfg.AuthMethod == "oauth" || cfg.AuthMethod == "token" || cfg.AuthMethod == "codex-cli" {
			provider, err := createCodexAuthProvider()
			if err != nil {
				return nil, "", err
			}
			return provider, modelID, nil
		}
		// OpenAI with API key
		if apiKey == "" && httpProviderRequiresAPIKey(protocol, apiBase) {
			return nil, "", fmt.Errorf("api_key is required for HTTP-based protocol %q (api_base=%q)", protocol, apiBase)
		}
		return NewHTTPProviderWithMaxTokensFieldAndRequestTimeout(
			apiKey,
			apiBase,
			cfg.Proxy,
			cfg.MaxTokensField,
			cfg.RequestTimeout,
		), modelID, nil

	case "litellm", "openrouter", "groq", "zhipu", "gemini", "nvidia",
		"ollama", "moonshot", "shengsuanyun", "deepseek", "cerebras",
		"volcengine", "vllm", "qwen", "mistral", "avian":
		// All other OpenAI-compatible HTTP providers
		apiBase := resolveHTTPProviderAPIBase(cfg, protocol)
		if apiKey == "" && httpProviderRequiresAPIKey(protocol, apiBase) {
			return nil, "", fmt.Errorf("api_key is required for HTTP-based protocol %q (api_base=%q)", protocol, apiBase)
		}
		return NewHTTPProviderWithMaxTokensFieldAndRequestTimeout(
			apiKey,
			apiBase,
			cfg.Proxy,
			cfg.MaxTokensField,
			cfg.RequestTimeout,
		), modelID, nil

	case "anthropic":
		if cfg.AuthMethod == "oauth" || cfg.AuthMethod == "token" {
			// Use OAuth credentials from auth store
			provider, err := createClaudeAuthProvider()
			if err != nil {
				return nil, "", err
			}
			return provider, modelID, nil
		}
		// Use API key with HTTP API
		apiBase := resolveHTTPProviderAPIBase(cfg, protocol)
		if apiKey == "" {
			return nil, "", fmt.Errorf("api_key is required for anthropic protocol (model: %s)", cfg.Model)
		}
		return NewHTTPProviderWithMaxTokensFieldAndRequestTimeout(
			apiKey,
			apiBase,
			cfg.Proxy,
			cfg.MaxTokensField,
			cfg.RequestTimeout,
		), modelID, nil

	case "antigravity":
		return NewAntigravityProvider(), modelID, nil

	case "claude-cli", "claudecli":
		workspace := cfg.Workspace
		if workspace == "" {
			workspace = "."
		}
		return NewClaudeCliProvider(workspace), modelID, nil

	case "codex-cli", "codexcli":
		workspace := cfg.Workspace
		if workspace == "" {
			workspace = "."
		}
		return NewCodexCliProvider(workspace), modelID, nil

	case "github-copilot", "copilot":
		apiBase := cfg.APIBase
		if apiBase == "" {
			apiBase = "localhost:4321"
		}
		connectMode := cfg.ConnectMode
		if connectMode == "" {
			connectMode = "grpc"
		}
		provider, err := NewGitHubCopilotProvider(apiBase, connectMode, modelID)
		if err != nil {
			return nil, "", err
		}
		return provider, modelID, nil

	default:
		return nil, "", fmt.Errorf("unknown protocol %q in model %q", protocol, cfg.Model)
	}
}
