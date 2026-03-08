package agent

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/xwysyy/X-Claw/pkg/config"
	"github.com/xwysyy/X-Claw/pkg/logger"
	"github.com/xwysyy/X-Claw/pkg/providers"
	"github.com/xwysyy/X-Claw/pkg/routing"
)

// resolveAgentIdentity extracts identity fields from agent config.
func resolveAgentIdentity(agentCfg *config.AgentConfig) (id, name string, skills []string) {
	if agentCfg == nil {
		return routing.DefaultAgentID, "", nil
	}
	return routing.NormalizeAgentID(agentCfg.ID), agentCfg.Name, agentCfg.Skills
}

// resolveFallbackCandidates builds the fallback candidate list from model config.
func resolveFallbackCandidates(model string, fallbacks []string, defaultProvider string, cfg *config.Config) []providers.FallbackCandidate {
	defaultProvider = strings.TrimSpace(defaultProvider)
	if defaultProvider == "" {
		defaultProvider = "openai"
	}
	modelCfg := providers.ModelConfig{
		Primary:   model,
		Fallbacks: fallbacks,
	}
	lookup := func(raw string) (string, bool) {
		raw = strings.TrimSpace(raw)
		if raw == "" || cfg == nil {
			return "", false
		}
		if mc, err := cfg.GetModelConfig(raw); err == nil && mc != nil && strings.TrimSpace(mc.Model) != "" {
			return providers.EnsureProtocol(mc.Model), true
		}
		for i := range cfg.ModelList {
			fullModel := strings.TrimSpace(cfg.ModelList[i].Model)
			if fullModel == "" {
				continue
			}
			if fullModel == raw {
				return providers.EnsureProtocol(fullModel), true
			}
			if _, modelID := providers.ExtractProtocol(fullModel); modelID == raw {
				return providers.EnsureProtocol(fullModel), true
			}
		}
		return "", false
	}
	return providers.ResolveCandidatesWithLookup(modelCfg, defaultProvider, lookup)
}

// resolveAgentWorkspace determines the workspace directory for an agent.
func resolveAgentWorkspace(agentCfg *config.AgentConfig, defaults *config.AgentDefaults) string {
	if agentCfg != nil && strings.TrimSpace(agentCfg.Workspace) != "" {
		return expandHome(strings.TrimSpace(agentCfg.Workspace))
	}
	if agentCfg == nil || agentCfg.Default || agentCfg.ID == "" || routing.NormalizeAgentID(agentCfg.ID) == "main" {
		return expandHome(defaults.Workspace)
	}
	home, _ := os.UserHomeDir()
	id := routing.NormalizeAgentID(agentCfg.ID)
	return filepath.Join(home, ".x-claw", "workspace-"+id)
}

// resolveAgentModel resolves the primary model for an agent.
func resolveAgentModel(agentCfg *config.AgentConfig, defaults *config.AgentDefaults) string {
	if agentCfg != nil && agentCfg.Model != nil && strings.TrimSpace(agentCfg.Model.Primary) != "" {
		return strings.TrimSpace(agentCfg.Model.Primary)
	}
	return defaults.GetModelName()
}

// resolveAgentFallbacks resolves the fallback models for an agent.
func resolveAgentFallbacks(agentCfg *config.AgentConfig, defaults *config.AgentDefaults) []string {
	if agentCfg != nil && agentCfg.Model != nil && agentCfg.Model.Fallbacks != nil {
		return agentCfg.Model.Fallbacks
	}
	return defaults.ModelFallbacks
}

func compilePatterns(patterns []string) []*regexp.Regexp {
	compiled := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			logger.WarnCF("agent", "Invalid path pattern", map[string]any{
				"pattern": p,
				"error":   err.Error(),
			})
			continue
		}
		compiled = append(compiled, re)
	}
	return compiled
}

func expandHome(path string) string {
	if path == "" {
		return path
	}
	if path[0] == '~' {
		home, _ := os.UserHomeDir()
		if len(path) > 1 && path[1] == '/' {
			return home + path[1:]
		}
		return home
	}
	return path
}

// CompactionSettings groups all compaction-related parameters.
type CompactionSettings struct {
	Mode                     string
	ReserveTokens            int
	KeepRecentTokens         int
	MaxHistoryShare          float64
	MemoryFlushEnabled       bool
	MemoryFlushSoftThreshold int
	NotifyUser               bool
}

// ContextPruningSettings groups all context pruning parameters.
type ContextPruningSettings struct {
	Mode              string
	IncludeChitChat   bool
	SoftToolChars     int
	HardToolChars     int
	TriggerRatio      float64
	BootstrapSnapshot bool
}

func resolveCompaction(c config.AgentCompactionConfig) CompactionSettings {
	mode := strings.TrimSpace(c.Mode)
	if mode == "" {
		mode = "safeguard"
	}

	flushEnabled := c.MemoryFlush.Enabled
	if !c.MemoryFlush.Enabled && c.MemoryFlush.SoftThresholdTokens == 0 {
		flushEnabled = true
	}

	return CompactionSettings{
		Mode:                     mode,
		ReserveTokens:            intDefault(c.ReserveTokens, 2048),
		KeepRecentTokens:         intDefault(c.KeepRecentTokens, 2048),
		MaxHistoryShare:          floatRangeDefault(c.MaxHistoryShare, 0, 0.9, 0.5),
		MemoryFlushEnabled:       flushEnabled,
		MemoryFlushSoftThreshold: intDefault(c.MemoryFlush.SoftThresholdTokens, 1500),
		NotifyUser:               c.NotifyUser,
	}
}

func resolveContextPruning(c config.AgentContextPruningConfig) ContextPruningSettings {
	mode := strings.TrimSpace(c.Mode)
	if mode == "" {
		mode = "tools_only"
	}
	return ContextPruningSettings{
		Mode:            mode,
		IncludeChitChat: c.IncludeOldChitChat,
		SoftToolChars:   intDefault(c.SoftToolResultChars, 2000),
		HardToolChars:   intDefault(c.HardToolResultChars, 350),
		TriggerRatio:    floatRangeDefault(c.TriggerRatio, 0, 1, 0.8),
	}
}

func resolveMemoryVector(c config.AgentMemoryVectorConfig) MemoryVectorSettings {
	apiKey := ""
	if c.Embedding.APIKey.Present() {
		if v, err := c.Embedding.APIKey.Resolve(""); err == nil {
			apiKey = v
		}
	}
	return MemoryVectorSettings{
		Enabled:         c.Enabled,
		Dimensions:      intDefault(c.Dimensions, defaultMemoryVectorDimensions),
		TopK:            intDefault(c.TopK, defaultMemoryVectorTopK),
		MinScore:        floatRangeDefault(c.MinScore, 0, 1, defaultMemoryVectorMinScore),
		MaxContextChars: intDefault(c.MaxContextChars, defaultMemoryVectorMaxContextChars),
		RecentDailyDays: intDefault(c.RecentDailyDays, defaultMemoryVectorRecentDailyDays),
		Embedding: MemoryVectorEmbeddingSettings{
			Kind:                  c.Embedding.Kind,
			APIKey:                apiKey,
			APIBase:               c.Embedding.APIBase,
			Model:                 c.Embedding.Model,
			Proxy:                 c.Embedding.Proxy,
			BatchSize:             c.Embedding.BatchSize,
			RequestTimeoutSeconds: c.Embedding.RequestTimeoutSeconds,
		},
		Hybrid: MemoryHybridSettings{
			FTSWeight:    c.Hybrid.FTSWeight,
			VectorWeight: c.Hybrid.VectorWeight,
		},
	}
}

func intDefault(v, fallback int) int {
	if v <= 0 {
		return fallback
	}
	return v
}

func floatRangeDefault(v, lo, hi, fallback float64) float64 {
	if v <= lo || v >= hi {
		return fallback
	}
	return v
}

// AgentRegistry manages multiple agent instances and routes messages to them.
type AgentRegistry struct {
	agents   map[string]*AgentInstance
	resolver *routing.RouteResolver
	mu       sync.RWMutex
}

// NewAgentRegistry creates a registry from config, instantiating all agents.
