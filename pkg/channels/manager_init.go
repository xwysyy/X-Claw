package channels

import (
	"strings"

	"github.com/xwysyy/X-Claw/pkg/config"
)

type channelInitSpec struct {
	name        string
	displayName string
	enabled     func(cfg *config.Config) bool
}

func selectedChannelInitializers(cfg *config.Config) []channelInitSpec {
	if cfg == nil {
		return nil
	}

	return []channelInitSpec{
		{
			name:        "telegram",
			displayName: "Telegram",
			enabled: func(cfg *config.Config) bool {
				return cfg.Channels.Telegram.Enabled && cfg.Channels.Telegram.Token.Present()
			},
		},
		{
			name:        "whatsapp_native",
			displayName: "WhatsApp Native",
			enabled: func(cfg *config.Config) bool {
				wa := cfg.Channels.WhatsApp
				return wa.Enabled && wa.UseNative
			},
		},
		{
			name:        "whatsapp",
			displayName: "WhatsApp",
			enabled: func(cfg *config.Config) bool {
				wa := cfg.Channels.WhatsApp
				return wa.Enabled && !wa.UseNative && strings.TrimSpace(wa.BridgeURL) != ""
			},
		},
		{
			name:        "feishu",
			displayName: "Feishu",
			enabled:     func(cfg *config.Config) bool { return cfg.Channels.Feishu.Enabled },
		},
		{
			name:        "discord",
			displayName: "Discord",
			enabled: func(cfg *config.Config) bool {
				return cfg.Channels.Discord.Enabled && cfg.Channels.Discord.Token.Present()
			},
		},
		{
			name:        "qq",
			displayName: "QQ",
			enabled:     func(cfg *config.Config) bool { return cfg.Channels.QQ.Enabled },
		},
		{
			name:        "dingtalk",
			displayName: "DingTalk",
			enabled: func(cfg *config.Config) bool {
				return cfg.Channels.DingTalk.Enabled && strings.TrimSpace(cfg.Channels.DingTalk.ClientID) != ""
			},
		},
		{
			name:        "slack",
			displayName: "Slack",
			enabled: func(cfg *config.Config) bool {
				return cfg.Channels.Slack.Enabled && cfg.Channels.Slack.BotToken.Present()
			},
		},
		{
			name:        "line",
			displayName: "LINE",
			enabled: func(cfg *config.Config) bool {
				return cfg.Channels.LINE.Enabled && cfg.Channels.LINE.ChannelAccessToken.Present()
			},
		},
		{
			name:        "onebot",
			displayName: "OneBot",
			enabled: func(cfg *config.Config) bool {
				return cfg.Channels.OneBot.Enabled && strings.TrimSpace(cfg.Channels.OneBot.WSUrl) != ""
			},
		},
		{
			name:        "wecom",
			displayName: "WeCom",
			enabled: func(cfg *config.Config) bool {
				return cfg.Channels.WeCom.Enabled && cfg.Channels.WeCom.Token.Present()
			},
		},
		{
			name:        "wecom_aibot",
			displayName: "WeCom AI Bot",
			enabled: func(cfg *config.Config) bool {
				return cfg.Channels.WeComAIBot.Enabled && cfg.Channels.WeComAIBot.Token.Present()
			},
		},
		{
			name:        "wecom_app",
			displayName: "WeCom App",
			enabled: func(cfg *config.Config) bool {
				return cfg.Channels.WeComApp.Enabled && strings.TrimSpace(cfg.Channels.WeComApp.CorpID) != ""
			},
		},
		{
			name:        "pico",
			displayName: "Pico",
			enabled: func(cfg *config.Config) bool {
				return cfg.Channels.Pico.Enabled && cfg.Channels.Pico.Token.Present()
			},
		},
	}
}
