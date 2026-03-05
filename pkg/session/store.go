package session

import "github.com/sipeed/picoclaw/internal/core/ports"

// Store is the session store port interface.
//
// It is re-exported from internal/core so callers can depend on pkg/session
// while the refactor is in progress.
type Store = ports.SessionStore
