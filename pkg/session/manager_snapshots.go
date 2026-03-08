package session

import (
	"sort"

	"github.com/xwysyy/X-Claw/pkg/providers"
	"github.com/xwysyy/X-Claw/pkg/utils"
)

func snapshotFromSession(stored *Session) Session {
	snapshot := Session{
		Key:                        stored.Key,
		Summary:                    stored.Summary,
		CompactionCount:            stored.CompactionCount,
		MemoryFlushAt:              stored.MemoryFlushAt,
		MemoryFlushCompactionCount: stored.MemoryFlushCompactionCount,
		Created:                    stored.Created,
		Updated:                    stored.Updated,
		ModelOverride:              stored.ModelOverride,
	}
	if stored.ModelOverrideExpiresAtMS != nil && *stored.ModelOverrideExpiresAtMS > 0 {
		expires := *stored.ModelOverrideExpiresAtMS
		snapshot.ModelOverrideExpiresAtMS = &expires
	}
	if len(stored.Messages) > 0 {
		snapshot.Messages = cloneMessages(stored.Messages)
	} else {
		snapshot.Messages = []providers.Message{}
	}
	return snapshot
}

func (sm *SessionManager) GetSessionSnapshot(key string) (*Session, bool) {
	key = utils.CanonicalSessionKey(key)
	if key == "" {
		return nil, false
	}

	sm.mu.RLock()
	defer sm.mu.RUnlock()

	stored, ok := sm.sessions[key]
	if !ok {
		return nil, false
	}
	snapshot := snapshotFromSession(stored)
	return &snapshot, true
}

func (sm *SessionManager) ListSessionSnapshots() []Session {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	snapshots := make([]Session, 0, len(sm.sessions))
	for _, stored := range sm.sessions {
		snapshots = append(snapshots, snapshotFromSession(stored))
	}

	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].Updated.After(snapshots[j].Updated)
	})
	return snapshots
}
