package agent

import (
	"path/filepath"
)

func (cb *ContextBuilder) memoryVectorSettingsLocked() MemoryVectorSettings {
	s := cb.settingsSnapshot()
	return MemoryVectorSettings{
		Enabled:         s.MemoryVectorEnabled,
		Dimensions:      s.MemoryVectorDimensions,
		TopK:            s.MemoryVectorTopK,
		MinScore:        s.MemoryVectorMinScore,
		MaxContextChars: s.MemoryVectorMaxChars,
		RecentDailyDays: s.MemoryVectorRecentDays,
		Embedding:       s.MemoryVectorEmbedding,
		Hybrid:          s.MemoryHybrid,
	}
}

func (cb *ContextBuilder) settingsSnapshot() ContextRuntimeSettings {
	if cb == nil {
		return ContextRuntimeSettings{}
	}
	cb.runtimeMu.RLock()
	defer cb.runtimeMu.RUnlock()
	return cb.settings
}

func (cb *ContextBuilder) runtimeSnapshot() contextRuntimeSnapshot {
	if cb == nil {
		return contextRuntimeSnapshot{}
	}
	cb.runtimeMu.RLock()
	defer cb.runtimeMu.RUnlock()
	return contextRuntimeSnapshot{
		settings:              cb.settings,
		tools:                 cb.tools,
		webEvidenceEnabled:    cb.webEvidenceEnabled,
		webEvidenceMinDomains: cb.webEvidenceMinDomains,
	}
}

func (cb *ContextBuilder) memoryScopesLimitLocked() int {
	if cb == nil || cb.memoryScopesMaxEntries <= 0 {
		return defaultContextMemoryScopesMaxEntries
	}
	return cb.memoryScopesMaxEntries
}

func (cb *ContextBuilder) touchMemoryScopeLocked(memoryDir string) {
	if cb.memoryScopesLastUsed == nil {
		cb.memoryScopesLastUsed = map[string]uint64{}
	}
	cb.memoryScopesUseTick++
	cb.memoryScopesLastUsed[memoryDir] = cb.memoryScopesUseTick
}

func (cb *ContextBuilder) evictMemoryScopesLocked(preserve string) {
	if cb == nil || len(cb.memoryScopes) == 0 {
		return
	}

	limit := cb.memoryScopesLimitLocked()
	if limit < 1 {
		limit = 1
	}

	for len(cb.memoryScopes) > limit {
		oldestPath := ""
		var oldestTick uint64

		for path, store := range cb.memoryScopes {
			if store == nil {
				delete(cb.memoryScopes, path)
				delete(cb.memoryScopesLastUsed, path)
				continue
			}
			if path == preserve && len(cb.memoryScopes) > 1 {
				continue
			}
			tick := cb.memoryScopesLastUsed[path]
			if oldestPath == "" || tick < oldestTick {
				oldestPath = path
				oldestTick = tick
			}
		}

		if oldestPath == "" {
			return
		}

		delete(cb.memoryScopes, oldestPath)
		delete(cb.memoryScopesLastUsed, oldestPath)
	}
}

// MemoryForSession returns the effective MemoryStore for the current session.
//
// This enables Phase B3 (scoped memory) by routing:
// - direct DM sessions -> user-scoped memory
// - group/channel sessions -> session-scoped memory
// - everything else -> agent-scoped memory (workspace/memory)
func (cb *ContextBuilder) MemoryForSession(sessionKey, channel, chatID string) *MemoryStore {
	if cb == nil {
		return nil
	}

	scope := deriveMemoryScope(sessionKey, channel, chatID)
	if scope.Kind == memoryScopeAgent {
		return cb.memory
	}

	token := memoryScopeToken(scope.RawID)
	memoryDir := filepath.Join(cb.workspace, "memory", "scopes", string(scope.Kind), token)
	vecSettings := cb.memoryVectorSettingsLocked()

	cb.memoryScopesMu.Lock()
	defer cb.memoryScopesMu.Unlock()

	if cb.memoryScopes == nil {
		cb.memoryScopes = map[string]*MemoryStore{}
	}
	if cb.memoryScopesLastUsed == nil {
		cb.memoryScopesLastUsed = map[string]uint64{}
	}
	if store, ok := cb.memoryScopes[memoryDir]; ok && store != nil {
		cb.touchMemoryScopeLocked(memoryDir)
		cb.evictMemoryScopesLocked(memoryDir)
		return store
	}

	store := NewMemoryStoreAt(memoryDir)
	store.SetVectorSettings(vecSettings)
	cb.memoryScopes[memoryDir] = store
	cb.touchMemoryScopeLocked(memoryDir)
	cb.evictMemoryScopesLocked(memoryDir)
	return store
}

// MemoryReadForSession returns a read-only memory view for the current session.
//
// For scoped sessions (user/session), reads are layered:
//   - agent memory (shared baseline)
//   - scoped memory (isolated overlay)
//
// This keeps durable global preferences available across channels, while still
// routing new writes (memory flush) into the scoped store to avoid pollution.
func (cb *ContextBuilder) MemoryReadForSession(sessionKey, channel, chatID string) MemoryReader {
	if cb == nil {
		return nil
	}

	scope := deriveMemoryScope(sessionKey, channel, chatID)
	if scope.Kind == memoryScopeAgent {
		return cb.memory
	}

	// Scoped store for this session (writes go here).
	scoped := cb.MemoryForSession(sessionKey, channel, chatID)
	if scoped == nil || cb.memory == nil || scoped == cb.memory {
		// Best-effort fallback.
		if scoped != nil {
			return scoped
		}
		return cb.memory
	}

	token := memoryScopeToken(scope.RawID)
	prefix := filepath.ToSlash(filepath.Join("scopes", string(scope.Kind), token))
	return &memoryReadStack{
		root:         cb.memory,
		scoped:       scoped,
		scopedKind:   scope.Kind,
		scopedPrefix: prefix,
	}
}
