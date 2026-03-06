package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/xwysyy/X-Claw/pkg/bus"
	"github.com/xwysyy/X-Claw/pkg/config"
	"github.com/xwysyy/X-Claw/pkg/logger"
	"github.com/xwysyy/X-Claw/pkg/providers"
	"github.com/xwysyy/X-Claw/pkg/skills"
	"github.com/xwysyy/X-Claw/pkg/tools"
	"github.com/xwysyy/X-Claw/pkg/utils"
)

type toolRegistrar struct {
	cfg              *config.Config
	msgBus           *bus.MessageBus
	registry         *AgentRegistry
	provider         providers.LLMProvider
	sessionsExecutor tools.SessionsSendExecutor
	taskLedger       *tools.TaskLedger
}

type sharedToolInstaller func(toolRegistrar, *AgentInstance, string)

func defaultSharedToolInstallers() []sharedToolInstaller {
	return []sharedToolInstaller{
		func(r toolRegistrar, agent *AgentInstance, _ string) { r.registerWebTools(agent) },
		func(r toolRegistrar, agent *AgentInstance, _ string) { r.registerMessageTool(agent) },
		func(r toolRegistrar, agent *AgentInstance, _ string) { r.registerConfirmTool(agent) },
		func(r toolRegistrar, agent *AgentInstance, _ string) { r.registerCalendarTool(agent) },
		func(r toolRegistrar, agent *AgentInstance, _ string) { r.registerSkillTools(agent) },
		func(r toolRegistrar, agent *AgentInstance, agentID string) { r.registerHandoffTools(agent, agentID) },
		func(r toolRegistrar, agent *AgentInstance, agentID string) { r.registerSpawnTools(agent, agentID) },
	}
}

func registerSharedTools(
	cfg *config.Config,
	msgBus *bus.MessageBus,
	registry *AgentRegistry,
	provider providers.LLMProvider,
	sessionsExecutor tools.SessionsSendExecutor,
	taskLedger *tools.TaskLedger,
) {
	registrar := toolRegistrar{
		cfg:              cfg,
		msgBus:           msgBus,
		registry:         registry,
		provider:         provider,
		sessionsExecutor: sessionsExecutor,
		taskLedger:       taskLedger,
	}

	for _, agentID := range registry.ListAgentIDs() {
		agent, ok := registry.GetAgent(agentID)
		if !ok {
			continue
		}

		for _, install := range defaultSharedToolInstallers() {
			install(registrar, agent, agentID)
		}
		agent.ContextBuilder.SetToolsRegistry(agent.Tools)
	}
}

func (r toolRegistrar) listAgents() []tools.AgentInfo {
	ids := r.registry.ListAgentIDs()
	sort.Strings(ids)
	out := make([]tools.AgentInfo, 0, len(ids))
	for _, id := range ids {
		agent, ok := r.registry.GetAgent(id)
		if !ok || agent == nil {
			continue
		}
		out = append(out, tools.AgentInfo{
			ID:        strings.TrimSpace(agent.ID),
			Name:      strings.TrimSpace(agent.Name),
			Model:     strings.TrimSpace(agent.Model),
			Workspace: strings.TrimSpace(agent.Workspace),
		})
	}
	return out
}

func (r toolRegistrar) lookupAgent(agentID string) (tools.AgentInfo, bool) {
	agent, ok := r.registry.GetAgent(agentID)
	if !ok || agent == nil {
		return tools.AgentInfo{}, false
	}
	return tools.AgentInfo{
		ID:        strings.TrimSpace(agent.ID),
		Name:      strings.TrimSpace(agent.Name),
		Model:     strings.TrimSpace(agent.Model),
		Workspace: strings.TrimSpace(agent.Workspace),
	}, true
}

func resolveSecretValue(label string, ref config.SecretRef) string {
	if !ref.Present() {
		return ""
	}
	v, err := ref.Resolve("")
	if err != nil {
		logger.WarnCF("agent", "Secret resolve failed (best-effort)", map[string]any{
			"secret": label,
			"error":  err.Error(),
		})
		return ""
	}
	return strings.TrimSpace(v)
}

func resolveSecretValueList(label string, refs []config.SecretRef) []string {
	if len(refs) == 0 {
		return nil
	}
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if v := resolveSecretValue(label, ref); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func buildWebSearchToolOptions(cfg *config.Config) tools.WebSearchToolOptions {
	return tools.WebSearchToolOptions{
		BraveAPIKey:          resolveSecretValue("tools.web.brave.api_key", cfg.Tools.Web.Brave.APIKey),
		BraveAPIKeys:         resolveSecretValueList("tools.web.brave.api_keys", cfg.Tools.Web.Brave.APIKeys),
		BraveMaxResults:      cfg.Tools.Web.Brave.MaxResults,
		BraveEnabled:         cfg.Tools.Web.Brave.Enabled,
		TavilyAPIKey:         resolveSecretValue("tools.web.tavily.api_key", cfg.Tools.Web.Tavily.APIKey),
		TavilyAPIKeys:        resolveSecretValueList("tools.web.tavily.api_keys", cfg.Tools.Web.Tavily.APIKeys),
		TavilyBaseURL:        cfg.Tools.Web.Tavily.BaseURL,
		TavilyMaxResults:     cfg.Tools.Web.Tavily.MaxResults,
		TavilyEnabled:        cfg.Tools.Web.Tavily.Enabled,
		DuckDuckGoMaxResults: cfg.Tools.Web.DuckDuckGo.MaxResults,
		DuckDuckGoEnabled:    cfg.Tools.Web.DuckDuckGo.Enabled,
		PerplexityAPIKey:     resolveSecretValue("tools.web.perplexity.api_key", cfg.Tools.Web.Perplexity.APIKey),
		PerplexityMaxResults: cfg.Tools.Web.Perplexity.MaxResults,
		PerplexityEnabled:    cfg.Tools.Web.Perplexity.Enabled,
		GLMSearchAPIKey:      resolveSecretValue("tools.web.glm_search.api_key", cfg.Tools.Web.GLMSearch.APIKey),
		GLMSearchBaseURL:     cfg.Tools.Web.GLMSearch.BaseURL,
		GLMSearchEngine:      cfg.Tools.Web.GLMSearch.SearchEngine,
		GLMSearchMaxResults:  cfg.Tools.Web.GLMSearch.MaxResults,
		GLMSearchEnabled:     cfg.Tools.Web.GLMSearch.Enabled,
		GrokAPIKey:           resolveSecretValue("tools.web.grok.api_key", cfg.Tools.Web.Grok.APIKey),
		GrokAPIKeys:          resolveSecretValueList("tools.web.grok.api_keys", cfg.Tools.Web.Grok.APIKeys),
		GrokEndpoint:         cfg.Tools.Web.Grok.Endpoint,
		GrokModel:            cfg.Tools.Web.Grok.DefaultModel,
		GrokMaxResults:       cfg.Tools.Web.Grok.MaxResults,
		GrokEnabled:          cfg.Tools.Web.Grok.Enabled,
		Proxy:                cfg.Tools.Web.Proxy,
		EvidenceModeEnabled:  cfg.Tools.Web.Evidence.Enabled,
		EvidenceMinDomains:   cfg.Tools.Web.Evidence.MinDomains,
	}
}

func (r toolRegistrar) registerWebTools(agent *AgentInstance) {
	if r.cfg == nil {
		return
	}
	webOpts := buildWebSearchToolOptions(r.cfg)
	if r.cfg.Tools.IsToolEnabled("web") {
		if searchTool := tools.NewWebSearchTool(webOpts); searchTool != nil {
			agent.Tools.Register(searchTool)
		}
		if dualSearchTool := tools.NewWebSearchDualTool(webOpts); dualSearchTool != nil {
			agent.Tools.Register(dualSearchTool)
		}
	}
	if !r.cfg.Tools.IsToolEnabled("web_fetch") {
		return
	}
	fetchTool, err := tools.NewWebFetchToolWithProxy(50000, r.cfg.Tools.Web.Proxy, r.cfg.Tools.Web.FetchLimitBytes)
	if err != nil {
		logger.ErrorCF("agent", "Failed to create web fetch tool", map[string]any{"error": err.Error()})
		return
	}
	agent.Tools.Register(fetchTool)
}

func (r toolRegistrar) registerMessageTool(agent *AgentInstance) {
	if r.cfg == nil || !r.cfg.Tools.IsToolEnabled("message") {
		return
	}
	messageTool := tools.NewMessageTool()
	messageTool.SetSendCallback(func(channel, chatID, content string) error {
		pubCtx, pubCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer pubCancel()
		return r.msgBus.PublishOutbound(pubCtx, bus.OutboundMessage{
			Channel: channel,
			ChatID:  chatID,
			Content: content,
		})
	})
	agent.Tools.Register(messageTool)
}

func (r toolRegistrar) registerConfirmTool(agent *AgentInstance) {
	if r.cfg == nil {
		return
	}
	confirmTTL := time.Duration(r.cfg.Tools.Policy.Confirm.ExpiresSeconds) * time.Second
	agent.Tools.Register(tools.NewToolConfirmTool(agent.Workspace, confirmTTL))
}

func (r toolRegistrar) registerCalendarTool(agent *AgentInstance) {
	if r.cfg == nil {
		return
	}
	if strings.TrimSpace(r.cfg.Channels.Feishu.AppID) == "" || !r.cfg.Channels.Feishu.AppSecret.Present() {
		return
	}
	agent.Tools.Register(tools.NewFeishuCalendarTool(r.cfg.Channels.Feishu))
}

func (r toolRegistrar) registerSkillTools(agent *AgentInstance) {
	if r.cfg == nil || !r.cfg.Tools.IsToolEnabled("skills") {
		return
	}
	findEnabled := r.cfg.Tools.IsToolEnabled("find_skills")
	installEnabled := r.cfg.Tools.IsToolEnabled("install_skill")
	if !findEnabled && !installEnabled {
		return
	}
	clawhubAuthToken := resolveSecretValue("tools.skills.registries.clawhub.auth_token", r.cfg.Tools.Skills.Registries.ClawHub.AuthToken)
	registryMgr := skills.NewRegistryManagerFromConfig(skills.RegistryConfig{
		MaxConcurrentSearches: r.cfg.Tools.Skills.MaxConcurrentSearches,
		ClawHub: skills.ClawHubConfig{
			Enabled:         r.cfg.Tools.Skills.Registries.ClawHub.Enabled,
			BaseURL:         r.cfg.Tools.Skills.Registries.ClawHub.BaseURL,
			AuthToken:       clawhubAuthToken,
			SearchPath:      r.cfg.Tools.Skills.Registries.ClawHub.SearchPath,
			SkillsPath:      r.cfg.Tools.Skills.Registries.ClawHub.SkillsPath,
			DownloadPath:    r.cfg.Tools.Skills.Registries.ClawHub.DownloadPath,
			Timeout:         r.cfg.Tools.Skills.Registries.ClawHub.Timeout,
			MaxZipSize:      r.cfg.Tools.Skills.Registries.ClawHub.MaxZipSize,
			MaxResponseSize: r.cfg.Tools.Skills.Registries.ClawHub.MaxResponseSize,
		},
	})
	if findEnabled {
		searchCache := skills.NewSearchCache(
			r.cfg.Tools.Skills.SearchCache.MaxSize,
			time.Duration(r.cfg.Tools.Skills.SearchCache.TTLSeconds)*time.Second,
		)
		agent.Tools.Register(tools.NewFindSkillsTool(registryMgr, searchCache))
	}
	if installEnabled {
		agent.Tools.Register(tools.NewInstallSkillTool(registryMgr, agent.Workspace))
	}
}

func (r toolRegistrar) registerHandoffTools(agent *AgentInstance, currentAgentID string) {
	agent.Tools.Register(tools.NewAgentsListTool(r.listAgents))
	handoffTool := tools.NewHandoffTool(agent.ID, agent.Sessions, r.lookupAgent)
	parentSubagents := agent.Subagents
	handoffTool.SetAllowlistChecker(func(_ string, targetAgentID string) bool {
		if parentSubagents == nil || parentSubagents.AllowAgents == nil {
			return true
		}
		if len(parentSubagents.AllowAgents) == 0 {
			return false
		}
		return r.registry.CanSpawnSubagent(currentAgentID, targetAgentID)
	})
	agent.Tools.Register(handoffTool)
}

func (r toolRegistrar) registerSpawnTools(agent *AgentInstance, currentAgentID string) {
	if r.cfg == nil || !r.cfg.Tools.IsToolEnabled("spawn") {
		return
	}
	if !r.cfg.Tools.IsToolEnabled("subagent") {
		logger.WarnCF("agent", "spawn tool requires subagent to be enabled", map[string]any{
			"agent_id": currentAgentID,
		})
		return
	}

	subagentManager := tools.NewSubagentManager(r.provider, agent.Model, agent.Workspace, r.msgBus)
	subagentManager.SetLLMOptions(agent.MaxTokens, agent.Temperature)
	subagentManager.SetLimits(
		r.cfg.Orchestration.MaxParallelWorkers,
		r.cfg.Orchestration.MaxTasksPerAgent,
		r.cfg.Orchestration.MaxSpawnDepth,
	)
	subagentManager.SetToolCallParallelism(
		r.cfg.Orchestration.ToolCallsParallelEnabled,
		r.cfg.Orchestration.MaxToolCallConcurrency,
		r.cfg.Orchestration.ParallelToolsMode,
		r.cfg.Orchestration.ToolParallelOverrides,
	)
	subagentManager.SetToolExecutionPolicy(r.cfg.Tools.Policy, r.cfg.Tools.Policy.Audit.Tags)
	subagentManager.SetToolExecutionTracing(
		tools.ToolTraceOptions{
			Enabled:               r.cfg.Tools.Trace.Enabled,
			Dir:                   r.cfg.Tools.Trace.Dir,
			WritePerCallFiles:     r.cfg.Tools.Trace.WritePerCallFiles,
			MaxArgPreviewChars:    r.cfg.Tools.Trace.MaxArgPreviewChars,
			MaxResultPreviewChars: r.cfg.Tools.Trace.MaxResultPreviewChars,
		},
		tools.ToolErrorTemplateOptions{
			Enabled:               r.cfg.Tools.ErrorTemplate.Enabled,
			IncludeSchema:         r.cfg.Tools.ErrorTemplate.IncludeSchema,
			IncludeAvailableTools: true,
		},
	)
	subagentManager.SetToolHooks(tools.BuildDefaultToolHooks(r.cfg))
	subagentManager.SetResourceBudgets(r.cfg.Limits)
	subagentManager.SetTools(agent.Tools)
	agent.SubagentManager = subagentManager
	subagentManager.SetExecutionResolver(func(targetAgentID string) (tools.SubagentExecutionConfig, error) {
		return resolveSubagentExecution(r.cfg, r.registry, r.provider, currentAgentID, targetAgentID)
	})
	if r.taskLedger != nil {
		subagentManager.SetEventHandler(func(event tools.SubagentTaskEvent) {
			handleSubagentTaskEvent(r.taskLedger, r.cfg, event)
		})
	}

	spawnTool := tools.NewSpawnTool(subagentManager)
	sessionsSpawnTool := tools.NewSessionsSpawnTool(subagentManager)
	allowlist := func(targetAgentID string) bool {
		return r.registry.CanSpawnSubagent(currentAgentID, targetAgentID)
	}
	spawnTool.SetAllowlistChecker(allowlist)
	sessionsSpawnTool.SetAllowlistChecker(allowlist)
	agent.Tools.Register(spawnTool)
	agent.Tools.Register(sessionsSpawnTool)

	if r.sessionsExecutor != nil {
		agent.Tools.Register(tools.NewSessionsSendTool(r.sessionsExecutor))
		return
	}
	logger.WarnCF("agent", "sessions_send tool disabled: executor unavailable", map[string]any{
		"agent_id": currentAgentID,
	})
}

func resolveSubagentExecution(
	cfg *config.Config,
	registry *AgentRegistry,
	fallbackProvider providers.LLMProvider,
	parentAgentID, targetAgentID string,
) (tools.SubagentExecutionConfig, error) {
	selectedAgentID := parentAgentID
	if strings.TrimSpace(targetAgentID) != "" {
		selectedAgentID = targetAgentID
	}

	targetAgent, ok := registry.GetAgent(selectedAgentID)
	if !ok || targetAgent == nil {
		return tools.SubagentExecutionConfig{}, fmt.Errorf("target agent %q not found", selectedAgentID)
	}

	execution := tools.SubagentExecutionConfig{
		Provider: fallbackProvider,
		Model:    targetAgent.Model,
		Tools:    targetAgent.Tools,
	}

	modelCfg, err := cfg.GetModelConfig(targetAgent.Model)
	if err != nil {
		if execution.Provider != nil {
			return execution, nil
		}
		return tools.SubagentExecutionConfig{}, err
	}

	cfgCopy := *modelCfg
	if cfgCopy.Workspace == "" {
		cfgCopy.Workspace = targetAgent.Workspace
	}

	resolvedProvider, resolvedModel, err := providers.CreateProviderFromConfig(&cfgCopy)
	if err != nil {
		if execution.Provider != nil {
			return execution, nil
		}
		return tools.SubagentExecutionConfig{}, err
	}
	if resolvedProvider != nil {
		execution.Provider = resolvedProvider
	}
	if resolvedModel != "" {
		execution.Model = resolvedModel
	}
	return execution, nil
}

func handleSubagentTaskEvent(ledger *tools.TaskLedger, cfg *config.Config, event tools.SubagentTaskEvent) {
	if ledger == nil {
		return
	}
	task := event.Task
	status := tools.TaskStatus(task.Status)
	if status == "" {
		status = tools.TaskStatusPlanned
	}

	var deadline *int64
	if cfg != nil && cfg.Orchestration.DefaultTaskTimeoutSeconds > 0 {
		d := task.Created + int64(cfg.Orchestration.DefaultTaskTimeoutSeconds)*1000
		deadline = &d
	}

	_ = ledger.UpsertTask(tools.TaskLedgerEntry{
		ID:            task.ID,
		ParentTaskID:  task.ParentTaskID,
		AgentID:       task.AgentID,
		Source:        "spawn",
		Intent:        task.Task,
		OriginChannel: task.OriginChannel,
		OriginChatID:  task.OriginChatID,
		Status:        status,
		CreatedAtMS:   task.Created,
		DeadlineAtMS:  deadline,
		Result:        task.Result,
		Error:         event.Err,
	})

	for _, tr := range event.Trace {
		_ = ledger.AddEvidence(task.ID, tools.TaskEvidence{
			TimestampMS:   event.Timestamp,
			Iteration:     tr.Iteration,
			ToolName:      tr.ToolName,
			Arguments:     tr.Arguments,
			ResultPreview: utils.Truncate(tr.Result, 400),
			IsError:       tr.IsError,
			DurationMS:    tr.DurationMS,
		})
	}
}
