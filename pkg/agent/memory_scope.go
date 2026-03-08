package agent

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/xwysyy/X-Claw/pkg/utils"
)

// MemoryReader is the minimal read-only surface area required by the agent
// prompt builder and memory tools.
//
// It is intentionally narrower than *MemoryStore so we can provide a composite
// view (agent + scoped) without changing how writes are routed.
type memoryScopeKind string

const (
	memoryScopeAgent   memoryScopeKind = "agent"
	memoryScopeUser    memoryScopeKind = "user"
	memoryScopeSession memoryScopeKind = "session"
)

type memoryScope struct {
	Kind  memoryScopeKind
	RawID string
}

func deriveMemoryScope(sessionKey, channel, chatID string) memoryScope {
	raw := utils.CanonicalSessionKey(sessionKey)
	lower := raw

	if lower == "" {
		return memoryScope{Kind: memoryScopeAgent, RawID: "agent"}
	}

	if idx := strings.Index(lower, ":direct:"); idx >= 0 {
		peer := strings.TrimSpace(raw[idx+len(":direct:"):])
		if peer == "" {
			peer = strings.TrimSpace(chatID)
		}
		if peer == "" {
			peer = raw
		}
		return memoryScope{Kind: memoryScopeUser, RawID: peer}
	}

	if idx := strings.Index(lower, ":group:"); idx >= 0 {
		peer := strings.TrimSpace(raw[idx+len(":group:"):])
		if peer == "" {
			peer = strings.TrimSpace(chatID)
		}
		ch := strings.TrimSpace(channel)
		if ch == "" {
			ch = extractChannelFromSessionKey(raw)
		}
		rawID := strings.Trim(strings.TrimSpace(ch)+":group:"+strings.TrimSpace(peer), ":")
		if rawID == "" {
			rawID = raw
		}
		return memoryScope{Kind: memoryScopeSession, RawID: rawID}
	}

	if idx := strings.Index(lower, ":channel:"); idx >= 0 {
		peer := strings.TrimSpace(raw[idx+len(":channel:"):])
		if peer == "" {
			peer = strings.TrimSpace(chatID)
		}
		ch := strings.TrimSpace(channel)
		if ch == "" {
			ch = extractChannelFromSessionKey(raw)
		}
		rawID := strings.Trim(strings.TrimSpace(ch)+":channel:"+strings.TrimSpace(peer), ":")
		if rawID == "" {
			rawID = raw
		}
		return memoryScope{Kind: memoryScopeSession, RawID: rawID}
	}

	// Default to agent-scoped memory for main/cron/heartbeat and similar runtime tasks.
	return memoryScope{Kind: memoryScopeAgent, RawID: "agent"}
}

func extractChannelFromSessionKey(sessionKey string) string {
	lower := utils.CanonicalSessionKey(sessionKey)
	if !strings.HasPrefix(lower, "agent:") {
		return ""
	}

	parts := strings.Split(lower, ":")
	if len(parts) < 4 {
		return ""
	}

	candidate := strings.TrimSpace(parts[2])
	if candidate == "" {
		return ""
	}

	kind := strings.TrimSpace(parts[3])
	switch kind {
	case "direct", "group", "channel":
		return candidate
	}

	// Per-account session keys: agent:<id>:<channel>:<account>:direct:<peer>
	if len(parts) >= 5 && strings.TrimSpace(parts[4]) == "direct" {
		return candidate
	}

	return ""
}

var memoryScopeTokenRe = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func memoryScopeToken(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "unknown"
	}

	sanitized := memoryScopeTokenRe.ReplaceAllString(raw, "_")
	sanitized = strings.Trim(sanitized, "._-")
	if sanitized == "" {
		sanitized = "unknown"
	}

	sum := sha1.Sum([]byte(raw))
	hash := hex.EncodeToString(sum[:])[:8]

	// Keep directory tokens reasonably short to avoid path length issues, but
	// always append a hash to prevent collisions from truncation/sanitization.
	const maxLen = 80
	maxBase := maxLen - 1 - len(hash) // "_" + hash
	if maxBase < 1 {
		maxBase = 1
	}
	if len(sanitized) > maxBase {
		sanitized = sanitized[:maxBase]
		sanitized = strings.TrimRight(sanitized, "._-")
		if sanitized == "" {
			sanitized = "unknown"
		}
	}

	return sanitized + "_" + hash
}

type MemoryReader interface {
	GetMemoryContext() string
	SearchRelevant(ctx context.Context, query string, topK int, minScore float64) ([]MemoryVectorHit, error)
	GetBySource(ctx context.Context, source string) (MemoryVectorHit, bool, error)
}

// memoryReadStack overlays a scoped memory store on top of the agent (root) store.
//
// Read behavior:
// - SearchRelevant: merges hits from agent + scoped stores (scoped sources are prefixed).
// - GetBySource: prefers explicit scoped-prefixed sources; otherwise resolves against agent store.
// - GetMemoryContext: concatenates agent + scoped contexts with clear headings.
//
// Write behavior is NOT implemented here; callers should continue to use the
// scoped *MemoryStore returned by ContextBuilder.MemoryForSession().
type memoryReadStack struct {
	root *MemoryStore

	scoped       *MemoryStore
	scopedKind   memoryScopeKind
	scopedPrefix string // e.g. "scopes/session/<token>" (relative to root memory dir)
}

func prefixMemorySource(prefix, source string) string {
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	source = strings.TrimSpace(source)
	if prefix == "" || source == "" {
		return source
	}

	filePart, anchor, _ := strings.Cut(source, "#")
	filePart = strings.TrimSpace(filePart)
	anchor = strings.TrimSpace(anchor)
	if filePart == "" {
		return source
	}

	prefixedFile := filepath.ToSlash(filepath.Join(prefix, filepath.FromSlash(filePart)))
	if anchor == "" {
		return prefixedFile
	}
	return prefixedFile + "#" + anchor
}

func stripMemorySourcePrefix(prefix, source string) (string, bool) {
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	source = strings.TrimSpace(source)
	if prefix == "" || source == "" {
		return "", false
	}

	filePart, anchor, _ := strings.Cut(source, "#")
	filePart = strings.TrimSpace(filePart)
	anchor = strings.TrimSpace(anchor)
	if filePart == "" {
		return "", false
	}

	want := prefix + "/"
	if !strings.HasPrefix(filePart, want) {
		return "", false
	}

	strippedFile := strings.TrimPrefix(filePart, want)
	if strippedFile == "" {
		return "", false
	}
	if anchor == "" {
		return strippedFile, true
	}
	return strippedFile + "#" + anchor, true
}

func (ms *memoryReadStack) GetMemoryContext() string {
	if ms == nil {
		return ""
	}

	rootCtx := ""
	if ms.root != nil {
		rootCtx = ms.root.GetMemoryContext()
	}

	scopedCtx := ""
	if ms.scoped != nil && ms.scoped != ms.root {
		scopedCtx = ms.scoped.GetMemoryContext()
	}

	if strings.TrimSpace(rootCtx) == "" {
		return scopedCtx
	}
	if strings.TrimSpace(scopedCtx) == "" {
		return rootCtx
	}

	kindLabel := "Scoped"
	switch ms.scopedKind {
	case memoryScopeUser:
		kindLabel = "User"
	case memoryScopeSession:
		kindLabel = "Session"
	}

	var sb strings.Builder
	sb.WriteString("## Agent Memory (shared)\n\n")
	sb.WriteString(strings.TrimSpace(rootCtx))
	sb.WriteString("\n\n---\n\n")
	sb.WriteString(fmt.Sprintf("## %s Memory (isolated)\n\n", kindLabel))
	sb.WriteString(strings.TrimSpace(scopedCtx))
	return strings.TrimSpace(sb.String())
}

func (ms *memoryReadStack) SearchRelevant(ctx context.Context, query string, topK int, minScore float64) ([]MemoryVectorHit, error) {
	if ms == nil {
		return nil, nil
	}

	var rootHits []MemoryVectorHit
	var rootErr error
	if ms.root != nil {
		rootHits, rootErr = ms.root.SearchRelevant(ctx, query, topK, minScore)
	}

	var scopedHits []MemoryVectorHit
	var scopedErr error
	if ms.scoped != nil && ms.scoped != ms.root {
		scopedHits, scopedErr = ms.scoped.SearchRelevant(ctx, query, topK, minScore)
		if ms.scopedPrefix != "" {
			for i := range scopedHits {
				scopedHits[i].Source = prefixMemorySource(ms.scopedPrefix, scopedHits[i].Source)
			}
		}
	}

	hits := mergeMemoryHits(rootHits, scopedHits, topK)
	if len(hits) == 0 && rootErr != nil && scopedErr != nil {
		return nil, fmt.Errorf("memory search unavailable: agent=%v; scoped=%v", rootErr, scopedErr)
	}
	return hits, nil
}

func (ms *memoryReadStack) GetBySource(ctx context.Context, source string) (MemoryVectorHit, bool, error) {
	if ms == nil {
		return MemoryVectorHit{}, false, nil
	}

	source = strings.TrimSpace(source)
	if source == "" {
		return MemoryVectorHit{}, false, nil
	}

	// 1) Explicit scoped path: "scopes/<kind>/<token>/..." — route to scoped store.
	if ms.scoped != nil && ms.scoped != ms.root && ms.scopedPrefix != "" {
		if stripped, ok := stripMemorySourcePrefix(ms.scopedPrefix, source); ok {
			hit, found, err := ms.scoped.GetBySource(ctx, stripped)
			if found && err == nil && hit.Source != "" {
				hit.Source = prefixMemorySource(ms.scopedPrefix, hit.Source)
			}
			return hit, found, err
		}
	}

	// 2) Default: resolve against agent memory.
	if ms.root != nil {
		hit, found, err := ms.root.GetBySource(ctx, source)
		if found || err != nil {
			return hit, found, err
		}
	}

	// 3) Backward-compat fallback: allow unprefixed scoped sources (older sessions).
	if ms.scoped != nil && ms.scoped != ms.root {
		hit, found, err := ms.scoped.GetBySource(ctx, source)
		if found && err == nil && hit.Source != "" && ms.scopedPrefix != "" {
			hit.Source = prefixMemorySource(ms.scopedPrefix, hit.Source)
		}
		return hit, found, err
	}

	return MemoryVectorHit{}, false, nil
}
