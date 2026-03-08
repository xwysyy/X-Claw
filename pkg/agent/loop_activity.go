package agent

import (
	"strings"

	"github.com/xwysyy/X-Claw/pkg/utils"
)

// RecordLastChannel records the last active channel for this workspace.
func (al *AgentLoop) RecordLastChannel(channel string) error {
	if al.state == nil {
		return nil
	}
	return al.state.SetLastChannel(channel)
}

// RecordLastChatID records the last active chat ID for this workspace.
func (al *AgentLoop) RecordLastChatID(chatID string) error {
	if al.state == nil {
		return nil
	}
	return al.state.SetLastChatID(chatID)
}

// RecordLastSessionKey records the last active external conversation session key.
func (al *AgentLoop) RecordLastSessionKey(sessionKey string) error {
	if al.state == nil {
		return nil
	}
	return al.state.SetLastSessionKey(utils.CanonicalSessionKey(sessionKey))
}

// LastActive returns the most recently used channel and chat ID for this workspace.
func (al *AgentLoop) LastActive() (string, string) {
	_, channel, chatID := al.LastActiveContext()
	return channel, chatID
}

// LastActiveContext returns the most recently used external session key, channel, and chat ID.
func (al *AgentLoop) LastActiveContext() (string, string, string) {
	if al == nil || al.state == nil {
		return "", "", ""
	}
	key := strings.TrimSpace(al.state.GetLastChannel())
	if key == "" {
		return "", "", ""
	}

	parts := strings.SplitN(key, ":", 2)
	if len(parts) != 2 {
		return "", "", ""
	}
	channel := strings.TrimSpace(parts[0])
	chatID := strings.TrimSpace(parts[1])
	if channel == "" || chatID == "" {
		return "", "", ""
	}
	return utils.CanonicalSessionKey(al.state.GetLastSessionKey()), channel, chatID
}
