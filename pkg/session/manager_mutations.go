package session

import (
	"strings"
	"time"

	"github.com/xwysyy/X-Claw/pkg/providers"
)

func (sm *SessionManager) ensureSessionLocked(key string) *Session {
	if session, ok := sm.sessions[key]; ok && session != nil {
		return session
	}

	now := time.Now()
	session := &Session{
		Key:      key,
		Messages: []providers.Message{},
		Created:  now,
		Updated:  now,
	}
	sm.sessions[key] = session
	return session
}

func cloneMessages(history []providers.Message) []providers.Message {
	if len(history) == 0 {
		return []providers.Message{}
	}
	msgs := make([]providers.Message, len(history))
	copy(msgs, history)
	return msgs
}

func (sm *SessionManager) newEventLocked(now time.Time, key string, session *Session, typ EventType) SessionEvent {
	return SessionEvent{
		Type:       typ,
		ID:         newEventID(),
		ParentID:   strings.TrimSpace(session.LastEventID),
		TS:         now.UTC().Format(time.RFC3339Nano),
		TSMS:       now.UnixMilli(),
		SessionKey: key,
	}
}

func (sm *SessionManager) persistEventAndMetaLocked(key string, session *Session, ev SessionEvent) {
	if sm.storage == "" {
		return
	}
	if path := sm.eventsPath(key); path != "" {
		if err := appendJSONLEvent(path, ev); err == nil {
			session.LastEventID = ev.ID
		}
	}
	sm.persistMetaLocked(key, session)
}

func (sm *SessionManager) persistMetaLocked(key string, session *Session) {
	if sm.storage == "" {
		return
	}
	if path := sm.metaPath(key); path != "" {
		_ = writeMetaFile(path, buildSessionMeta(session))
	}
}
