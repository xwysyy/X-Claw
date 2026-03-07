package config

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/xwysyy/X-Claw/pkg/fileutil"
	"github.com/xwysyy/X-Claw/pkg/utils"
)

func isLoopbackHost(host string) bool {
	return utils.IsLoopbackAddr(host)
}

func (c *Config) ValidateSecurity() error {
	if c == nil {
		return nil
	}

	problems := c.securityProblems()
	if len(problems) == 0 {
		return nil
	}

	msgs := make([]string, 0, len(problems))
	for _, p := range problems {
		if strings.TrimSpace(p.Message) != "" {
			msgs = append(msgs, strings.TrimSpace(p.Message))
		}
	}
	if len(msgs) == 0 {
		return fmt.Errorf("unsafe configuration (break-glass required)")
	}
	return fmt.Errorf("unsafe configuration (break-glass required): %s", strings.Join(msgs, "; "))
}

func (c *Config) migrateChannelConfigs() {
	if c.Channels.Discord.MentionOnly && !c.Channels.Discord.GroupTrigger.MentionOnly {
		c.Channels.Discord.GroupTrigger.MentionOnly = true
	}

	if len(c.Channels.OneBot.GroupTriggerPrefix) > 0 &&
		len(c.Channels.OneBot.GroupTrigger.Prefixes) == 0 {
		c.Channels.OneBot.GroupTrigger.Prefixes = c.Channels.OneBot.GroupTriggerPrefix
	}
}

func SaveConfig(path string, cfg *Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(path, data, 0o600)
}
