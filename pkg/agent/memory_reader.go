package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// MemoryReader is the minimal read-only surface area required by the agent
// prompt builder and memory tools.
//
// It is intentionally narrower than *MemoryStore so we can provide a composite
// view (agent + scoped) without changing how writes are routed.
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
