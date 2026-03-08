package providers

import "strings"

// ModelRef represents a parsed model reference with provider and model name.
type ModelRef struct {
	Provider string
	Model    string
}

// ParseModelRef parses "anthropic/claude-opus" into {Provider: "anthropic", Model: "claude-opus"}.
// If no slash present, uses defaultProvider.
// Returns nil for empty input.
func ParseModelRef(raw string, defaultProvider string) *ModelRef {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	if idx := strings.Index(raw, "/"); idx > 0 {
		provider := NormalizeProvider(raw[:idx])
		model := strings.TrimSpace(raw[idx+1:])
		if model == "" {
			return nil
		}
		return &ModelRef{Provider: provider, Model: model}
	}

	return &ModelRef{
		Provider: NormalizeProvider(defaultProvider),
		Model:    raw,
	}
}

// NormalizeProvider normalizes provider identifiers to canonical form.
func NormalizeProvider(provider string) string {
	p := strings.ToLower(strings.TrimSpace(provider))

	switch p {
	case "z.ai", "z-ai":
		return "zai"
	case "opencode-zen":
		return "opencode"
	case "qwen":
		return "qwen-portal"
	case "kimi-code":
		return "kimi-coding"
	case "gpt":
		return "openai"
	case "claude":
		return "anthropic"
	case "glm":
		return "zhipu"
	case "google":
		return "gemini"
	}

	return p
}

// CanonicalProtocol maps provider/protocol aliases to the canonical protocol names
// used by ModelConfig.Model and HTTP/default API-base resolution.
func CanonicalProtocol(provider string) string {
	switch NormalizeProvider(provider) {
	case "", "openai":
		return "openai"
	case "zai", "zhipu":
		return "zhipu"
	case "qwen-portal":
		return "qwen"
	case "github-copilot", "github_copilot", "copilot":
		return "github-copilot"
	case "claude-cli", "claudecli":
		return "claude-cli"
	case "codex-cli", "codexcli":
		return "codex-cli"
	default:
		return NormalizeProvider(provider)
	}
}

// EnsureProtocol prefixes bare model names with the default canonical protocol,
// and canonicalizes any existing protocol alias.
func EnsureProtocol(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return model
	}
	protocol, modelID, found := strings.Cut(model, "/")
	if !found {
		return "openai/" + model
	}
	return CanonicalProtocol(protocol) + "/" + strings.TrimSpace(modelID)
}

// ModelKey returns a canonical "provider/model" key for deduplication.
func ModelKey(provider, model string) string {
	return NormalizeProvider(provider) + "/" + strings.ToLower(strings.TrimSpace(model))
}
