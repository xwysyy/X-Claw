package session

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xwysyy/X-Claw/pkg/logger"
	"github.com/xwysyy/X-Claw/pkg/utils"
)

func sanitizeFilename(key string) string {
	return strings.ReplaceAll(key, ":", "_")
}

func fileStemForKey(key string) (string, bool) {
	stem := sanitizeFilename(utils.CanonicalSessionKey(key))
	if stem == "" || stem == "." {
		return "", false
	}
	if !filepath.IsLocal(stem) {
		return "", false
	}
	if strings.ContainsAny(stem, `/\\`) {
		return "", false
	}
	return stem, true
}

func (sm *SessionManager) eventsPath(key string) string {
	if sm == nil {
		return ""
	}
	stem, ok := fileStemForKey(key)
	if !ok {
		return ""
	}
	return filepath.Join(sm.storage, stem+".jsonl")
}

func (sm *SessionManager) metaPath(key string) string {
	if sm == nil {
		return ""
	}
	stem, ok := fileStemForKey(key)
	if !ok {
		return ""
	}
	return filepath.Join(sm.storage, stem+".meta.json")
}

func (sm *SessionManager) legacySnapshotPath(key string) string {
	if sm == nil {
		return ""
	}
	stem, ok := fileStemForKey(key)
	if !ok {
		return ""
	}
	return filepath.Join(sm.storage, stem+".json")
}

func buildSessionMeta(s *Session) SessionMeta {
	meta := SessionMeta{
		Key:           s.Key,
		Summary:       s.Summary,
		Created:       s.Created,
		Updated:       s.Updated,
		LastEventID:   strings.TrimSpace(s.LastEventID),
		MessagesCount: len(s.Messages),
		ModelOverride: strings.TrimSpace(s.ModelOverride),
	}
	if s.ModelOverrideExpiresAtMS != nil && *s.ModelOverrideExpiresAtMS > 0 {
		expires := *s.ModelOverrideExpiresAtMS
		meta.ModelOverrideExpiresAtMS = &expires
	}
	return meta
}

func (sm *SessionManager) Save(key string) error {
	if sm.storage == "" {
		return nil
	}

	key = utils.CanonicalSessionKey(key)
	if key == "" {
		return os.ErrInvalid
	}
	if _, ok := fileStemForKey(key); !ok {
		return os.ErrInvalid
	}

	sm.mu.RLock()
	stored, ok := sm.sessions[key]
	if !ok || stored == nil {
		sm.mu.RUnlock()
		return nil
	}
	meta := buildSessionMeta(stored)
	sm.mu.RUnlock()

	return writeMetaFile(sm.metaPath(key), meta)
}

func (sm *SessionManager) migrateLegacyToJSONL(sess *Session) error {
	if sm == nil || strings.TrimSpace(sm.storage) == "" || sess == nil {
		return nil
	}
	key := utils.CanonicalSessionKey(sess.Key)
	if key == "" {
		return nil
	}
	jsonlPath := sm.eventsPath(key)
	if strings.TrimSpace(jsonlPath) == "" {
		return nil
	}
	if _, err := os.Stat(jsonlPath); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	now := time.Now()
	parent := strings.TrimSpace(sess.LastEventID)
	for _, msg := range sess.Messages {
		msgCopy := msg
		ev := SessionEvent{
			Type:       EventSessionMessage,
			ID:         newEventID(),
			ParentID:   parent,
			TS:         now.UTC().Format(time.RFC3339Nano),
			TSMS:       now.UnixMilli(),
			SessionKey: key,
			Message:    &msgCopy,
		}
		if err := appendJSONLEvent(jsonlPath, ev); err != nil {
			return err
		}
		parent = ev.ID
		sess.LastEventID = ev.ID
	}

	if strings.TrimSpace(sess.Summary) != "" {
		ev := SessionEvent{
			Type:       EventSessionSummary,
			ID:         newEventID(),
			ParentID:   parent,
			TS:         now.UTC().Format(time.RFC3339Nano),
			TSMS:       now.UnixMilli(),
			SessionKey: key,
			Summary:    sess.Summary,
		}
		if err := appendJSONLEvent(jsonlPath, ev); err != nil {
			return err
		}
		sess.LastEventID = ev.ID
	}

	if path := sm.metaPath(key); path != "" {
		if err := writeMetaFile(path, buildSessionMeta(sess)); err != nil {
			logger.WarnCF("session", "Failed to persist migrated session meta", map[string]any{
				"key":   key,
				"path":  path,
				"error": err.Error(),
			})
		}
	}
	return nil
}
