package providers

import (
	"net/url"
	"strings"

	"github.com/xwysyy/X-Claw/pkg/config"
)

func resolveHTTPProviderAPIBase(cfg *config.ModelConfig, protocol string) string {
	if cfg == nil {
		return getDefaultAPIBase(protocol)
	}
	if apiBase := strings.TrimSpace(cfg.APIBase); apiBase != "" {
		return apiBase
	}
	return getDefaultAPIBase(protocol)
}

func isLocalAPIBase(apiBase string) bool {
	apiBase = strings.TrimSpace(apiBase)
	if apiBase == "" {
		return false
	}
	u, err := url.Parse(apiBase)
	if err != nil {
		return false
	}
	hostname := strings.ToLower(strings.TrimSpace(u.Hostname()))
	return hostname == "localhost" || hostname == "127.0.0.1" || hostname == "::1"
}

func httpProviderRequiresAPIKey(protocol, apiBase string) bool {
	switch protocol {
	case "litellm", "ollama", "vllm":
		return !isLocalAPIBase(apiBase)
	default:
		return true
	}
}

func getDefaultAPIBase(protocol string) string {
	protocol = CanonicalProtocol(protocol)
	switch protocol {
	case "openai":
		return "https://api.openai.com/v1"
	case "anthropic":
		return defaultAnthropicAPIBase
	case "openrouter":
		return "https://openrouter.ai/api/v1"
	case "litellm":
		return "http://localhost:4000/v1"
	case "groq":
		return "https://api.groq.com/openai/v1"
	case "zhipu":
		return "https://open.bigmodel.cn/api/paas/v4"
	case "gemini":
		return "https://generativelanguage.googleapis.com/v1beta"
	case "nvidia":
		return "https://integrate.api.nvidia.com/v1"
	case "ollama":
		return "http://localhost:11434/v1"
	case "moonshot":
		return "https://api.moonshot.cn/v1"
	case "shengsuanyun":
		return "https://router.shengsuanyun.com/api/v1"
	case "deepseek":
		return "https://api.deepseek.com/v1"
	case "cerebras":
		return "https://api.cerebras.ai/v1"
	case "volcengine":
		return "https://ark.cn-beijing.volces.com/api/v3"
	case "qwen":
		return "https://dashscope.aliyuncs.com/compatible-mode/v1"
	case "vllm":
		return "http://localhost:8000/v1"
	case "mistral":
		return "https://api.mistral.ai/v1"
	case "avian":
		return "https://api.avian.io/v1"
	default:
		return ""
	}
}
