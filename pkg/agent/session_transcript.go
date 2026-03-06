package agent

import (
	"strings"

	"github.com/xwysyy/X-Claw/pkg/logger"
	"github.com/xwysyy/X-Claw/pkg/providers"
	"github.com/xwysyy/X-Claw/pkg/session"
)

func addSessionMessage(store session.Store, sessionKey, role, content string) {
	if store == nil || strings.TrimSpace(sessionKey) == "" {
		return
	}
	store.AddMessage(sessionKey, role, content)
}

func addSessionMessageAndSave(store session.Store, sessionKey, role, content, warnMessage string, fields map[string]any) {
	addSessionMessage(store, sessionKey, role, content)
	saveSessionBestEffort(store, sessionKey, warnMessage, fields)
}

func addSessionFullMessage(store session.Store, sessionKey string, msg providers.Message) {
	if store == nil || strings.TrimSpace(sessionKey) == "" {
		return
	}
	store.AddFullMessage(sessionKey, msg)
}

func saveSessionBestEffort(store session.Store, sessionKey, warnMessage string, fields map[string]any) {
	if store == nil || strings.TrimSpace(sessionKey) == "" {
		return
	}
	if err := store.Save(sessionKey); err != nil {
		payload := map[string]any{"session_key": sessionKey, "error": err.Error()}
		for key, value := range fields {
			payload[key] = value
		}
		logger.WarnCF("agent", warnMessage, payload)
	}
}
