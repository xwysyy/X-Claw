package session

import (
	"sync"
	"time"

	coresession "github.com/xwysyy/X-Claw/internal/core/session"
	"github.com/xwysyy/X-Claw/pkg/providers"
	"github.com/xwysyy/X-Claw/pkg/utils"
)

// Type aliases keep existing imports stable while moving canonical session domain
// types into internal/core.
type Session = coresession.Session

type SessionManager struct {
	sessions    map[string]*Session
	mu          sync.RWMutex
	storage     string
	maxSessions int
	ttl         time.Duration
}

func (sm *SessionManager) GetOrCreate(key string) *Session {
	key = utils.CanonicalSessionKey(key)
	if key == "" {
		return nil
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	if session, ok := sm.sessions[key]; ok {
		return session
	}

	sm.pruneSessionsLocked(time.Now(), 1)
	return sm.ensureSessionLocked(key)
}

func (sm *SessionManager) AddMessage(sessionKey, role, content string) {
	sm.AddFullMessage(sessionKey, providers.Message{
		Role:    role,
		Content: content,
	})
}

// AddFullMessage adds a complete message with tool calls and tool call ID to the session.
// This is used to save the full conversation flow including tool calls and tool results.
func (sm *SessionManager) AddFullMessage(sessionKey string, msg providers.Message) {
	sessionKey = utils.CanonicalSessionKey(sessionKey)
	if sessionKey == "" {
		return
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	now := time.Now()
	if _, ok := sm.sessions[sessionKey]; !ok {
		sm.pruneSessionsLocked(now, 1)
	}
	session := sm.ensureSessionLocked(sessionKey)
	if session.Created.IsZero() {
		session.Created = now
	}
	storedMsg := cloneMessage(msg)
	session.Messages = append(session.Messages, storedMsg)
	session.Updated = now

	if sm.storage == "" {
		return
	}

	msgCopy := cloneMessage(msg)
	ev := sm.newEventLocked(now, sessionKey, session, EventSessionMessage)
	ev.Message = &msgCopy
	sm.persistEventAndMetaLocked(sessionKey, session, ev)
}

func (sm *SessionManager) GetHistory(key string) []providers.Message {
	key = utils.CanonicalSessionKey(key)
	if key == "" {
		return []providers.Message{}
	}

	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[key]
	if !ok {
		return []providers.Message{}
	}

	return cloneMessages(session.Messages)
}

func (sm *SessionManager) GetSummary(key string) string {
	key = utils.CanonicalSessionKey(key)
	if key == "" {
		return ""
	}

	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[key]
	if !ok {
		return ""
	}
	return session.Summary
}

func (sm *SessionManager) SetSummary(key string, summary string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	key = utils.CanonicalSessionKey(key)
	if key == "" {
		return
	}

	session, ok := sm.sessions[key]
	if ok {
		now := time.Now()
		session.Summary = summary
		session.Updated = now

		if sm.storage == "" {
			return
		}

		ev := sm.newEventLocked(now, key, session, EventSessionSummary)
		ev.Summary = summary
		sm.persistEventAndMetaLocked(key, session, ev)
	}
}

func (sm *SessionManager) TruncateHistory(key string, keepLast int) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	key = utils.CanonicalSessionKey(key)
	if key == "" {
		return
	}

	session, ok := sm.sessions[key]
	if !ok {
		return
	}

	now := time.Now()
	if keepLast <= 0 {
		session.Messages = []providers.Message{}
		session.Updated = now
	} else {
		if len(session.Messages) <= keepLast {
			return
		}
		session.Messages = session.Messages[len(session.Messages)-keepLast:]
		session.Updated = now
	}

	if sm.storage == "" {
		return
	}

	ev := sm.newEventLocked(now, key, session, EventSessionHistoryTrunc)
	ev.KeepLast = keepLast
	sm.persistEventAndMetaLocked(key, session, ev)
}

func (sm *SessionManager) IncrementCompactionCount(key string) int {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	key = utils.CanonicalSessionKey(key)
	if key == "" {
		return 0
	}

	session, ok := sm.sessions[key]
	if !ok {
		return 0
	}
	session.CompactionCount++
	now := time.Now()
	session.Updated = now

	if sm.storage != "" {
		ev := sm.newEventLocked(now, key, session, EventSessionCompactionInc)
		ev.CompactionCount = session.CompactionCount
		sm.persistEventAndMetaLocked(key, session, ev)
	}
	return session.CompactionCount
}

func (sm *SessionManager) MarkMemoryFlush(key string, compactionCount int) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	key = utils.CanonicalSessionKey(key)
	if key == "" {
		return
	}

	session, ok := sm.sessions[key]
	if !ok {
		return
	}
	now := time.Now()
	session.MemoryFlushAt = now
	session.MemoryFlushCompactionCount = compactionCount
	session.Updated = now

	if sm.storage != "" {
		ev := sm.newEventLocked(now, key, session, EventSessionMemoryFlush)
		ev.MemoryFlushAt = session.MemoryFlushAt
		ev.MemoryFlushCompactionCount = session.MemoryFlushCompactionCount
		sm.persistEventAndMetaLocked(key, session, ev)
	}
}

func (sm *SessionManager) GetCompactionState(key string) (count int, flushCount int, flushAt time.Time) {
	key = utils.CanonicalSessionKey(key)
	if key == "" {
		return 0, 0, time.Time{}
	}

	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[key]
	if !ok {
		return 0, 0, time.Time{}
	}
	return session.CompactionCount, session.MemoryFlushCompactionCount, session.MemoryFlushAt
}

// SetHistory updates the messages of a session.
func (sm *SessionManager) SetHistory(key string, history []providers.Message) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	key = utils.CanonicalSessionKey(key)
	if key == "" {
		return
	}

	session, ok := sm.sessions[key]
	if ok {
		msgs := cloneMessages(history)
		session.Messages = msgs
		now := time.Now()
		session.Updated = now

		if sm.storage == "" {
			return
		}

		ev := sm.newEventLocked(now, key, session, EventSessionHistorySet)
		ev.History = msgs
		sm.persistEventAndMetaLocked(key, session, ev)
	}
}
