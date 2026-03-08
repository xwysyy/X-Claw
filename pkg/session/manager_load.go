package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xwysyy/X-Claw/pkg/logger"
	"github.com/xwysyy/X-Claw/pkg/providers"
	"github.com/xwysyy/X-Claw/pkg/utils"
)

type sessionFileSet struct {
	base   string
	meta   string
	jsonl  string
	legacy string
}

func (sm *SessionManager) loadSessions() error {
	files, err := os.ReadDir(sm.storage)
	if err != nil {
		return err
	}

	byBase := groupSessionFiles(files, sm.storage)
	for _, sf := range byBase {
		if sf == nil {
			continue
		}
		if sm.loadJSONLSession(sf) {
			continue
		}
		sm.loadLegacySession(sf)
	}
	return nil
}

func groupSessionFiles(files []os.DirEntry, storage string) map[string]*sessionFileSet {
	byBase := map[string]*sessionFileSet{}
	get := func(base string) *sessionFileSet {
		if base == "" {
			return nil
		}
		sf := byBase[base]
		if sf == nil {
			sf = &sessionFileSet{base: base}
			byBase[base] = sf
		}
		return sf
	}

	for _, ent := range files {
		if ent == nil || ent.IsDir() {
			continue
		}
		name := strings.TrimSpace(ent.Name())
		if name == "" || strings.HasPrefix(name, ".") {
			continue
		}
		lower := strings.ToLower(name)
		switch {
		case strings.HasSuffix(lower, ".meta.json"):
			base := strings.TrimSuffix(name, name[len(name)-len(".meta.json"):])
			if sf := get(base); sf != nil {
				sf.meta = filepath.Join(storage, name)
			}
		case strings.HasSuffix(lower, ".jsonl"):
			base := strings.TrimSuffix(name, name[len(name)-len(".jsonl"):])
			if sf := get(base); sf != nil {
				sf.jsonl = filepath.Join(storage, name)
			}
		case strings.HasSuffix(lower, ".json"):
			if strings.HasSuffix(lower, ".meta.json") {
				continue
			}
			base := strings.TrimSuffix(name, name[len(name)-len(".json"):])
			if sf := get(base); sf != nil {
				sf.legacy = filepath.Join(storage, name)
			}
		}
	}
	return byBase
}

func loadSessionMeta(path string) (*SessionMeta, error) {
	if strings.TrimSpace(path) == "" {
		return nil, os.ErrNotExist
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var meta SessionMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	if strings.TrimSpace(meta.Key) == "" {
		return nil, fmt.Errorf("meta missing key")
	}
	return &meta, nil
}

func loadLegacySession(path string) (*Session, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var sess Session
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, err
	}
	if strings.TrimSpace(sess.Key) == "" {
		return nil, fmt.Errorf("legacy snapshot missing key")
	}
	if sess.Messages == nil {
		sess.Messages = []providers.Message{}
	}
	return &sess, nil
}

func applySessionEvents(sess *Session, path, leafHint string) error {
	if sess == nil {
		return fmt.Errorf("session is nil")
	}
	if strings.TrimSpace(path) == "" {
		return os.ErrNotExist
	}
	events, err := readJSONLEvents(path)
	if err != nil {
		return err
	}

	replayed, effectiveLeaf := replayEvents(events, leafHint)
	if effectiveLeaf == "" {
		return nil
	}

	sess.Messages = replayed.Messages
	sess.Summary = replayed.Summary
	sess.CompactionCount = replayed.CompactionCount
	sess.MemoryFlushAt = replayed.MemoryFlushAt
	sess.MemoryFlushCompactionCount = replayed.MemoryFlushCompactionCount
	sess.LastEventID = effectiveLeaf
	if sess.Created.IsZero() && !replayed.Created.IsZero() {
		sess.Created = replayed.Created
	}
	if sess.Updated.IsZero() && !replayed.Updated.IsZero() {
		sess.Updated = replayed.Updated
	} else if !replayed.Updated.IsZero() && sess.Updated.Before(replayed.Updated) {
		sess.Updated = replayed.Updated
	}
	return nil
}

func (sm *SessionManager) loadJSONLSession(sf *sessionFileSet) bool {
	if sf == nil || strings.TrimSpace(sf.jsonl) == "" {
		return false
	}

	var meta *SessionMeta
	if m, err := loadSessionMeta(sf.meta); err == nil {
		meta = m
	}

	key := ""
	if meta != nil {
		key = strings.TrimSpace(meta.Key)
	}
	if key == "" && meta == nil && strings.TrimSpace(sf.legacy) != "" {
		if legacy, err := loadLegacySession(sf.legacy); err == nil {
			key = strings.TrimSpace(legacy.Key)
		}
	}
	if key == "" {
		key = sf.base
	}

	sess := &Session{Key: key, Messages: []providers.Message{}}
	if meta != nil {
		sess.Summary = strings.TrimSpace(meta.Summary)
		sess.ModelOverride = strings.TrimSpace(meta.ModelOverride)
		if meta.ModelOverrideExpiresAtMS != nil && *meta.ModelOverrideExpiresAtMS > 0 {
			expires := *meta.ModelOverrideExpiresAtMS
			sess.ModelOverrideExpiresAtMS = &expires
		}
		sess.Created = meta.Created
		sess.Updated = meta.Updated
		sess.LastEventID = strings.TrimSpace(meta.LastEventID)
	}

	if err := applySessionEvents(sess, sf.jsonl, sess.LastEventID); err != nil {
		logger.WarnCF("session", "Failed to apply JSONL events", map[string]any{
			"key":   sess.Key,
			"path":  sf.jsonl,
			"error": err.Error(),
		})
		return true
	}
	if sess.Created.IsZero() {
		sess.Created = time.Now()
	}
	if sess.Updated.IsZero() {
		sess.Updated = sess.Created
	}

	sess.Key = utils.CanonicalSessionKey(sess.Key)
	if sess.Key != "" {
		sm.sessions[sess.Key] = sess
	}
	return true
}

func (sm *SessionManager) loadLegacySession(sf *sessionFileSet) {
	if sf == nil || strings.TrimSpace(sf.legacy) == "" {
		return
	}
	legacy, err := loadLegacySession(sf.legacy)
	if err != nil {
		return
	}
	if sm.storage != "" {
		if err := sm.migrateLegacyToJSONL(legacy); err != nil {
			logger.WarnCF("session", "Legacy migration failed", map[string]any{
				"key":   legacy.Key,
				"error": err.Error(),
			})
		}
	}
	legacy.Key = utils.CanonicalSessionKey(legacy.Key)
	if legacy.Key != "" {
		sm.sessions[legacy.Key] = legacy
	}
}
