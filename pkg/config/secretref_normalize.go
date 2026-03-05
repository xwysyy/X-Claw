package config

import (
	"path/filepath"
	"strings"
)

// NormalizeSecretRefs expands/cleans SecretRef fields after loading config.json.
//
// Currently this:
// - trims whitespace
// - expands "~/" in file paths
// - rewrites relative file paths to be relative to config.json's directory
//
// This is intentionally best-effort and never returns an error; resolution errors
// are surfaced by the caller when a specific secret is required for an enabled
// surface (channel/provider/tool).
func (c *Config) NormalizeSecretRefs() {
	if c == nil {
		return
	}

	baseDir := ""
	if strings.TrimSpace(c.SourcePath) != "" {
		baseDir = filepath.Dir(strings.TrimSpace(c.SourcePath))
	}

	// Agents defaults
	c.Agents.Defaults.MemoryVector.Embedding.APIKey = c.Agents.Defaults.MemoryVector.Embedding.APIKey.Normalize(baseDir)

	// Gateway
	c.Gateway.APIKey = c.Gateway.APIKey.Normalize(baseDir)

	// Channels
	c.Channels.Telegram.Token = c.Channels.Telegram.Token.Normalize(baseDir)
	c.Channels.Feishu.AppSecret = c.Channels.Feishu.AppSecret.Normalize(baseDir)
	c.Channels.Feishu.EncryptKey = c.Channels.Feishu.EncryptKey.Normalize(baseDir)
	c.Channels.Feishu.VerificationToken = c.Channels.Feishu.VerificationToken.Normalize(baseDir)
	c.Channels.Discord.Token = c.Channels.Discord.Token.Normalize(baseDir)
	c.Channels.QQ.AppSecret = c.Channels.QQ.AppSecret.Normalize(baseDir)
	c.Channels.DingTalk.ClientSecret = c.Channels.DingTalk.ClientSecret.Normalize(baseDir)
	c.Channels.Slack.BotToken = c.Channels.Slack.BotToken.Normalize(baseDir)
	c.Channels.Slack.AppToken = c.Channels.Slack.AppToken.Normalize(baseDir)
	c.Channels.LINE.ChannelSecret = c.Channels.LINE.ChannelSecret.Normalize(baseDir)
	c.Channels.LINE.ChannelAccessToken = c.Channels.LINE.ChannelAccessToken.Normalize(baseDir)
	c.Channels.OneBot.AccessToken = c.Channels.OneBot.AccessToken.Normalize(baseDir)
	c.Channels.WeCom.Token = c.Channels.WeCom.Token.Normalize(baseDir)
	c.Channels.WeCom.EncodingAESKey = c.Channels.WeCom.EncodingAESKey.Normalize(baseDir)
	c.Channels.WeComApp.CorpSecret = c.Channels.WeComApp.CorpSecret.Normalize(baseDir)
	c.Channels.WeComApp.Token = c.Channels.WeComApp.Token.Normalize(baseDir)
	c.Channels.WeComApp.EncodingAESKey = c.Channels.WeComApp.EncodingAESKey.Normalize(baseDir)
	c.Channels.WeComAIBot.Token = c.Channels.WeComAIBot.Token.Normalize(baseDir)
	c.Channels.WeComAIBot.EncodingAESKey = c.Channels.WeComAIBot.EncodingAESKey.Normalize(baseDir)
	c.Channels.Pico.Token = c.Channels.Pico.Token.Normalize(baseDir)

	// Audit log
	c.AuditLog.HMACKey = c.AuditLog.HMACKey.Normalize(baseDir)

	// Providers (legacy section)
	c.Providers.Anthropic.APIKey = c.Providers.Anthropic.APIKey.Normalize(baseDir)
	c.Providers.OpenAI.APIKey = c.Providers.OpenAI.APIKey.Normalize(baseDir)
	c.Providers.LiteLLM.APIKey = c.Providers.LiteLLM.APIKey.Normalize(baseDir)
	c.Providers.OpenRouter.APIKey = c.Providers.OpenRouter.APIKey.Normalize(baseDir)
	c.Providers.Groq.APIKey = c.Providers.Groq.APIKey.Normalize(baseDir)
	c.Providers.Zhipu.APIKey = c.Providers.Zhipu.APIKey.Normalize(baseDir)
	c.Providers.VLLM.APIKey = c.Providers.VLLM.APIKey.Normalize(baseDir)
	c.Providers.Gemini.APIKey = c.Providers.Gemini.APIKey.Normalize(baseDir)
	c.Providers.Nvidia.APIKey = c.Providers.Nvidia.APIKey.Normalize(baseDir)
	c.Providers.Ollama.APIKey = c.Providers.Ollama.APIKey.Normalize(baseDir)
	c.Providers.Moonshot.APIKey = c.Providers.Moonshot.APIKey.Normalize(baseDir)
	c.Providers.ShengSuanYun.APIKey = c.Providers.ShengSuanYun.APIKey.Normalize(baseDir)
	c.Providers.DeepSeek.APIKey = c.Providers.DeepSeek.APIKey.Normalize(baseDir)
	c.Providers.Cerebras.APIKey = c.Providers.Cerebras.APIKey.Normalize(baseDir)
	c.Providers.VolcEngine.APIKey = c.Providers.VolcEngine.APIKey.Normalize(baseDir)
	c.Providers.GitHubCopilot.APIKey = c.Providers.GitHubCopilot.APIKey.Normalize(baseDir)
	c.Providers.Antigravity.APIKey = c.Providers.Antigravity.APIKey.Normalize(baseDir)
	c.Providers.Qwen.APIKey = c.Providers.Qwen.APIKey.Normalize(baseDir)
	c.Providers.Mistral.APIKey = c.Providers.Mistral.APIKey.Normalize(baseDir)

	// Model list
	for i := range c.ModelList {
		c.ModelList[i].APIKey = c.ModelList[i].APIKey.Normalize(baseDir)
	}

	// Tools: web search
	c.Tools.Web.Brave.APIKey = c.Tools.Web.Brave.APIKey.Normalize(baseDir)
	for i := range c.Tools.Web.Brave.APIKeys {
		c.Tools.Web.Brave.APIKeys[i] = c.Tools.Web.Brave.APIKeys[i].Normalize(baseDir)
	}
	c.Tools.Web.Tavily.APIKey = c.Tools.Web.Tavily.APIKey.Normalize(baseDir)
	for i := range c.Tools.Web.Tavily.APIKeys {
		c.Tools.Web.Tavily.APIKeys[i] = c.Tools.Web.Tavily.APIKeys[i].Normalize(baseDir)
	}
	c.Tools.Web.Grok.APIKey = c.Tools.Web.Grok.APIKey.Normalize(baseDir)
	for i := range c.Tools.Web.Grok.APIKeys {
		c.Tools.Web.Grok.APIKeys[i] = c.Tools.Web.Grok.APIKeys[i].Normalize(baseDir)
	}

	// Skills registries
	c.Tools.Skills.Registries.ClawHub.AuthToken = c.Tools.Skills.Registries.ClawHub.AuthToken.Normalize(baseDir)
}
