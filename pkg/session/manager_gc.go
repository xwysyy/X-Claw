package session

import (
	"os"
	"sort"
	"time"

	"github.com/xwysyy/X-Claw/pkg/config"
	"github.com/xwysyy/X-Claw/pkg/logger"
)

func NewSessionManager(storage string) *SessionManager {
	return newSessionManagerWithSessionConfig(storage, config.SessionConfig{})
}

func NewSessionManagerWithConfig(storage string, sessionCfg config.SessionConfig) *SessionManager {
	return newSessionManagerWithSessionConfig(storage, sessionCfg)
}

func newSessionManagerWithSessionConfig(storage string, sessionCfg config.SessionConfig) *SessionManager {
	return newSessionManagerWithGC(storage, sessionCfg.EffectiveMaxSessions(), sessionCfg.EffectiveTTL())
}

func newSessionManagerWithGC(storage string, maxSessions int, ttl time.Duration) *SessionManager {
	if maxSessions < 0 {
		maxSessions = 0
	}
	if ttl < 0 {
		ttl = 0
	}

	sm := &SessionManager{
		sessions:    make(map[string]*Session),
		storage:     storage,
		maxSessions: maxSessions,
		ttl:         ttl,
	}

	if storage != "" {
		if err := os.MkdirAll(storage, 0o755); err != nil {
			logger.WarnCF("session", "Failed to create session storage directory", map[string]any{
				"storage": storage,
				"error":   err.Error(),
			})
		}
		sm.loadSessions()
	}

	sm.mu.Lock()
	sm.pruneSessionsLocked(time.Now(), 0)
	sm.mu.Unlock()

	return sm
}

func (sm *SessionManager) pruneSessionsLocked(now time.Time, reserve int) {
	if sm == nil {
		return
	}

	ttlEvicted := 0
	if sm.ttl > 0 {
		for key, session := range sm.sessions {
			if session == nil {
				delete(sm.sessions, key)
				ttlEvicted++
				continue
			}
			updatedAt := session.Updated
			if updatedAt.IsZero() {
				updatedAt = session.Created
			}
			if updatedAt.IsZero() || now.Sub(updatedAt) <= sm.ttl {
				continue
			}
			delete(sm.sessions, key)
			ttlEvicted++
		}
		if ttlEvicted > 0 {
			logger.InfoCF("session", "Evicted expired sessions from memory", map[string]any{
				"count":     ttlEvicted,
				"ttl_hours": int(sm.ttl / time.Hour),
			})
		}
	}

	if sm.maxSessions <= 0 {
		return
	}

	allowed := sm.maxSessions - reserve
	if allowed < 0 {
		allowed = 0
	}
	if len(sm.sessions) <= allowed {
		return
	}

	type sessionEntry struct {
		key     string
		updated time.Time
	}

	entries := make([]sessionEntry, 0, len(sm.sessions))
	for key, session := range sm.sessions {
		updatedAt := time.Time{}
		if session != nil {
			updatedAt = session.Updated
			if updatedAt.IsZero() {
				updatedAt = session.Created
			}
		}
		entries = append(entries, sessionEntry{key: key, updated: updatedAt})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].updated.Before(entries[j].updated)
	})

	toEvict := len(sm.sessions) - allowed
	lruEvicted := 0
	for i := 0; i < toEvict && i < len(entries); i++ {
		delete(sm.sessions, entries[i].key)
		lruEvicted++
	}
	if lruEvicted > 0 {
		logger.InfoCF("session", "Evicted least recently updated sessions from memory", map[string]any{
			"count":        lruEvicted,
			"max_sessions": sm.maxSessions,
		})
	}
}
