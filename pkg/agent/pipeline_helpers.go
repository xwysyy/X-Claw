package agent

import (
	"fmt"
	"strings"

	"github.com/xwysyy/X-Claw/pkg/config"
	"github.com/xwysyy/X-Claw/pkg/logger"
	"github.com/xwysyy/X-Claw/pkg/providers"
	"github.com/xwysyy/X-Claw/pkg/session"
)

func loadRunHistory(agent *AgentInstance, opts processOptions) ([]providers.Message, string) {
	if opts.NoHistory {
		return nil, ""
	}
	return agent.Sessions.GetHistory(opts.SessionKey), agent.Sessions.GetSummary(opts.SessionKey)
}

func buildRunUserMessage(cfg *config.Config, opts processOptions) string {
	if !opts.PlanMode {
		return opts.UserMessage
	}
	restricted := "exec/edit_file/write_file/append_file"
	if cfg != nil && len(cfg.Tools.PlanMode.RestrictedTools) > 0 {
		restricted = strings.Join(cfg.Tools.PlanMode.RestrictedTools, ", ")
	}
	return fmt.Sprintf(
		"[PLAN MODE]\nYou are currently in PLAN mode for this session.\n"+
			"- You MUST NOT call restricted tools (%s).\n"+
			"- Draft a plan, ask the user to approve execution (/approve or /run), then stop.\n\n"+
			"USER REQUEST:\n%s",
		restricted,
		opts.UserMessage,
	)
}

func resolveMaxMediaSize(cfg *config.Config) int {
	if cfg == nil {
		return config.DefaultMaxMediaSize
	}
	return cfg.Agents.Defaults.GetMaxMediaSize()
}

func resolveRunModel(agent *AgentInstance, sessionKey string) string {
	modelForRun := strings.TrimSpace(agent.Model)
	if agent.Sessions != nil {
		if override, ok := agent.Sessions.EffectiveModelOverride(sessionKey); ok {
			modelForRun = override
		}
	}
	return modelForRun
}

func persistUserMessage(agent *AgentInstance, opts processOptions) {
	addSessionMessageAndSave(
		agent.Sessions,
		opts.SessionKey,
		"user",
		opts.UserMessage,
		"Failed to WAL user message (best-effort)",
		nil,
	)
}

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
