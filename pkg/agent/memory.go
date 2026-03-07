// X-Claw - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 X-Claw contributors

package agent

import (
	"bytes"
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/xwysyy/X-Claw/pkg/fileutil"
	"github.com/xwysyy/X-Claw/pkg/logger"
	"github.com/xwysyy/X-Claw/pkg/tools"
	"github.com/xwysyy/X-Claw/pkg/utils"
	"hash/fnv"
	"io"
	iofs "io/fs"
	"math"
	_ "modernc.org/sqlite"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// MemoryStore manages persistent memory for the agent.
// - Long-term memory: memory/MEMORY.md
// - Daily notes: memory/YYYYMM/YYYYMMDD.md
type MemoryStore struct {
	memoryDir  string
	memoryFile string
	vector     *memoryVectorStore
	fts        *memoryFTSStore
	settings   MemoryVectorSettings
}

var memorySectionOrder = []string{
	"Profile",
	"Long-term Facts",
	"Active Goals",
	"Constraints",
	"Open Threads",
	"Deprecated/Resolved",
}

var memorySectionAliases = map[string]string{
	"profile":             "Profile",
	"long-term memory":    "Long-term Facts",
	"long term memory":    "Long-term Facts",
	"long-term facts":     "Long-term Facts",
	"active goals":        "Active Goals",
	"constraints":         "Constraints",
	"open threads":        "Open Threads",
	"open tasks":          "Open Threads",
	"pending tasks":       "Open Threads",
	"deprecated/resolved": "Deprecated/Resolved",
	"resolved":            "Deprecated/Resolved",
}

// NewMemoryStore creates a new MemoryStore with the given workspace path.
// It ensures the memory directory exists.
func NewMemoryStore(workspace string) *MemoryStore {
	return NewMemoryStoreAt(filepath.Join(workspace, "memory"))
}

// NewMemoryStoreAt creates a new MemoryStore rooted at memoryDir.
//
// memoryDir is expected to contain MEMORY.md and daily notes (YYYYMM/YYYYMMDD.md).
// This is used for scoped memory (per-user/per-group/per-session) within an agent workspace.
func NewMemoryStoreAt(memoryDir string) *MemoryStore {
	memoryFile := filepath.Join(memoryDir, "MEMORY.md")

	// Ensure memory directory exists
	_ = os.MkdirAll(memoryDir, 0o755)

	vectorSettings := defaultMemoryVectorSettings()
	vectorSettings = normalizeMemoryVectorSettings(vectorSettings)

	return &MemoryStore{
		memoryDir:  memoryDir,
		memoryFile: memoryFile,
		vector:     newMemoryVectorStore(memoryDir, memoryFile, vectorSettings),
		fts:        newMemoryFTSStore(memoryDir, memoryFile, vectorSettings),
		settings:   vectorSettings,
	}
}

// getTodayFile returns the path to today's daily note file (memory/YYYYMM/YYYYMMDD.md).
func (ms *MemoryStore) getTodayFile() string {
	today := time.Now().Format("20060102") // YYYYMMDD
	monthDir := today[:6]                  // YYYYMM
	filePath := filepath.Join(ms.memoryDir, monthDir, today+".md")
	return filePath
}

// ReadLongTerm reads the long-term memory (MEMORY.md).
// Returns empty string if the file doesn't exist.
func (ms *MemoryStore) ReadLongTerm() string {
	if data, err := os.ReadFile(ms.memoryFile); err == nil {
		return string(data)
	}
	return ""
}

// WriteLongTerm writes content to the long-term memory file (MEMORY.md).
func (ms *MemoryStore) WriteLongTerm(content string) error {
	// Use unified atomic write utility with explicit sync for flash storage reliability.
	// Using 0o600 (owner read/write only) for secure default permissions.
	if err := fileutil.WriteFileAtomic(ms.memoryFile, []byte(content), 0o600); err != nil {
		return err
	}
	ms.refreshVectorIndex()
	return nil
}

// ReadToday reads today's daily note.
// Returns empty string if the file doesn't exist.
func (ms *MemoryStore) ReadToday() string {
	todayFile := ms.getTodayFile()
	if data, err := os.ReadFile(todayFile); err == nil {
		return string(data)
	}
	return ""
}

// AppendToday appends content to today's daily note.
// If the file doesn't exist, it creates a new file with a date header.
func (ms *MemoryStore) AppendToday(content string) error {
	todayFile := ms.getTodayFile()

	// Ensure month directory exists
	monthDir := filepath.Dir(todayFile)
	if err := os.MkdirAll(monthDir, 0o755); err != nil {
		return err
	}

	var existingContent string
	if data, err := os.ReadFile(todayFile); err == nil {
		existingContent = string(data)
	}

	var newContent string
	if existingContent == "" {
		// Add header for new day
		header := fmt.Sprintf("# %s\n\n", time.Now().Format("2006-01-02"))
		newContent = header + content
	} else {
		// Append to existing content
		newContent = existingContent + "\n" + content
	}

	// Use unified atomic write utility with explicit sync for flash storage reliability.
	if err := fileutil.WriteFileAtomic(todayFile, []byte(newContent), 0o600); err != nil {
		return err
	}
	ms.refreshVectorIndex()
	return nil
}

// GetRecentDailyNotes returns daily notes from the last N days.
// Contents are joined with "---" separator.
func (ms *MemoryStore) GetRecentDailyNotes(days int) string {
	var sb strings.Builder
	first := true

	for i := range days {
		date := time.Now().AddDate(0, 0, -i)
		dateStr := date.Format("20060102") // YYYYMMDD
		monthDir := dateStr[:6]            // YYYYMM
		filePath := filepath.Join(ms.memoryDir, monthDir, dateStr+".md")

		if data, err := os.ReadFile(filePath); err == nil {
			if !first {
				sb.WriteString("\n\n---\n\n")
			}
			sb.Write(data)
			first = false
		}
	}

	return sb.String()
}

// GetMemoryContext returns formatted memory context for the agent prompt.
// Includes long-term memory and recent daily notes.
func (ms *MemoryStore) GetMemoryContext() string {
	longTerm := ms.ReadLongTerm()
	recentNotes := ms.GetRecentDailyNotes(3)

	if longTerm == "" && recentNotes == "" {
		return ""
	}

	var sb strings.Builder

	if longTerm != "" {
		sb.WriteString("## Long-term Memory\n\n")
		sb.WriteString(longTerm)
	}

	if recentNotes != "" {
		if longTerm != "" {
			sb.WriteString("\n\n---\n\n")
		}
		sb.WriteString("## Recent Daily Notes\n\n")
		sb.WriteString(recentNotes)
	}

	return sb.String()
}

// OrganizeWriteback rewrites MEMORY.md using stable blocks with guardrails:
// - persona/human/projects/facts
// - read-only protection for core blocks
// - hard character limits to prevent prompt bloat
func (ms *MemoryStore) OrganizeWriteback(extracted string) error {
	base := parseMemoryAsBlocks(ms.ReadLongTerm())
	incoming := parseMemoryAsBlocks(extracted)

	for _, spec := range memoryBlockSpecs {
		label := spec.Label
		base[label] = sanitizeMemoryText(base[label])
		incoming[label] = sanitizeMemoryText(incoming[label])
	}

	for _, spec := range memoryBlockSpecs {
		label := spec.Label
		if spec.ReadOnly {
			if strings.TrimSpace(base[label]) != "" && spec.Limit > 0 {
				base[label] = truncateRunes(strings.TrimSpace(base[label]), spec.Limit)
			}
			continue
		}

		entries := mergeBlockEntries(base[label], incoming[label])
		entries = clipEntriesToLimit(entries, spec.Limit)
		base[label] = renderEntries(entries)
	}

	return ms.WriteLongTerm(renderMemoryBlocks(base))
}

func (ms *MemoryStore) SetVectorSettings(settings MemoryVectorSettings) {
	settings = normalizeMemoryVectorSettings(settings)
	ms.settings = settings
	if ms.vector == nil {
		// still allow FTS-only settings updates
	} else {
		ms.vector.SetSettings(settings)
	}
	if ms.fts != nil {
		ms.fts.SetSettings(settings)
	}
}

// SearchRelevant runs semantic retrieval over MEMORY.md + recent daily notes.
func (ms *MemoryStore) SearchRelevant(ctx context.Context, query string, topK int, minScore float64) ([]MemoryVectorHit, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}

	var ftsHits []MemoryVectorHit
	var ftsErr error
	if ms.fts != nil {
		ftsHits, ftsErr = ms.fts.Search(ctx, query, topK)
	}

	var vecHits []MemoryVectorHit
	var vecErr error
	if ms.vector != nil {
		vecHits, vecErr = ms.vector.Search(ctx, query, topK, minScore)
	}

	hits := mergeHybridMemoryHits(ftsHits, vecHits, topK, ms.settings.Hybrid)
	if len(hits) == 0 && ftsErr != nil && vecErr != nil {
		return nil, fmt.Errorf("memory search unavailable: fts=%v; vector=%v", ftsErr, vecErr)
	}

	// Best-effort: return whatever we have. Vector embedding failures should not
	// take down deterministic keyword lookup (FTS), and vice versa.
	return hits, nil
}

func (ms *MemoryStore) GetBySource(ctx context.Context, source string) (MemoryVectorHit, bool, error) {
	_ = ctx

	src := strings.TrimSpace(source)
	if src == "" {
		return MemoryVectorHit{}, false, nil
	}

	filePart, anchor, _ := strings.Cut(src, "#")
	filePart = strings.TrimSpace(filePart)
	anchor = strings.TrimSpace(anchor)
	if filePart == "" {
		return MemoryVectorHit{}, false, nil
	}

	rel := filepath.Clean(filepath.FromSlash(filePart))
	if rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return MemoryVectorHit{}, false, fmt.Errorf("invalid memory source path %q", filePart)
	}

	path := filepath.Join(ms.memoryDir, rel)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return MemoryVectorHit{}, false, nil
		}
		return MemoryVectorHit{}, false, err
	}
	content := string(data)
	if strings.TrimSpace(content) == "" {
		return MemoryVectorHit{}, false, nil
	}

	if strings.EqualFold(rel, "MEMORY.md") {
		if anchor == "" {
			return MemoryVectorHit{Source: filePart, Text: content, Score: 1}, true, nil
		}

		label := ""
		if spec, ok := lookupMemoryBlockSpec(anchor); ok {
			label = spec.Label
		} else if section, ok := normalizeMemorySectionName(anchor); ok {
			if mapped, ok := memoryBlockLabelForLegacySection(section); ok {
				label = mapped
			} else {
				return MemoryVectorHit{}, false, nil
			}
		} else {
			return MemoryVectorHit{}, false, nil
		}

		blocks := parseMemoryAsBlocks(content)
		blockContent := strings.TrimSpace(blocks[label])
		if blockContent == "" {
			return MemoryVectorHit{}, false, nil
		}

		canonicalPath := filepath.ToSlash(rel)
		out := strings.TrimSpace("## " + label + "\n" + blockContent)
		if out == "" {
			return MemoryVectorHit{}, false, nil
		}
		return MemoryVectorHit{Source: fmt.Sprintf("%s#%s", canonicalPath, label), Text: out, Score: 1}, true, nil
	}

	// Non-MEMORY.md sources use chunk indexes for stable retrieval.
	if anchor == "" {
		return MemoryVectorHit{Source: filePart, Text: content, Score: 1}, true, nil
	}

	chunkIdx, convErr := parsePositiveInt(anchor)
	if convErr != nil || chunkIdx <= 0 {
		return MemoryVectorHit{}, false, nil
	}

	chunks := chunkMarkdownForVectors(content, memoryVectorChunkChars)
	if chunkIdx-1 >= len(chunks) {
		return MemoryVectorHit{}, false, nil
	}
	chunk := strings.TrimSpace(chunks[chunkIdx-1])
	if chunk == "" {
		return MemoryVectorHit{}, false, nil
	}

	return MemoryVectorHit{Source: fmt.Sprintf("%s#%d", filePart, chunkIdx), Text: chunk, Score: 1}, true, nil
}

func (ms *MemoryStore) refreshVectorIndex() {
	if ms.vector == nil {
		// still allow FTS-only
	} else {
		ms.vector.MarkDirty()
	}
	if ms.fts != nil {
		ms.fts.MarkDirty()
	}
}

func parseMemorySections(content string) map[string][]string {
	sections := make(map[string][]string, len(memorySectionOrder))
	if strings.TrimSpace(content) == "" {
		return sections
	}

	current := "Long-term Facts"
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			heading := strings.TrimSpace(strings.TrimLeft(line, "#"))
			if normalized, ok := normalizeMemorySectionName(heading); ok {
				current = normalized
			}
			continue
		}

		entry := strings.TrimSpace(strings.TrimLeft(line, "-*+"))
		if entry == "" {
			continue
		}
		sections[current] = append(sections[current], entry)
	}
	return sections
}

func normalizeMemorySectionName(name string) (string, bool) {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return "", false
	}
	if section, ok := memorySectionAliases[key]; ok {
		return section, true
	}
	for _, section := range memorySectionOrder {
		if strings.EqualFold(section, name) {
			return section, true
		}
	}
	return "", false
}

func normalizeMemorySections(sections map[string][]string) {
	for section, entries := range sections {
		seen := map[string]struct{}{}
		deduped := make([]string, 0, len(entries))
		for _, entry := range entries {
			clean := strings.TrimSpace(entry)
			if clean == "" {
				continue
			}
			key := strings.ToLower(clean)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			deduped = append(deduped, clean)
		}
		sort.Strings(deduped)
		sections[section] = deduped
	}
}

func renderMemorySections(sections map[string][]string) string {
	var sb strings.Builder
	sb.WriteString("# MEMORY\n\n")
	sb.WriteString(fmt.Sprintf("_Last organized: %s_\n\n", time.Now().Format("2006-01-02 15:04")))

	wroteSection := false
	for _, section := range memorySectionOrder {
		entries := sections[section]
		if len(entries) == 0 {
			continue
		}
		wroteSection = true
		sb.WriteString("## ")
		sb.WriteString(section)
		sb.WriteString("\n")
		for _, entry := range entries {
			sb.WriteString("- ")
			sb.WriteString(entry)
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	if !wroteSection {
		sb.WriteString("## Long-term Facts\n")
		sb.WriteString("- (no durable facts recorded yet)\n")
	}

	return strings.TrimSpace(sb.String()) + "\n"
}

func parsePositiveInt(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty int")
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid int %q", s)
	}
	return n, nil
}

func mergeMemoryHits(a, b []MemoryVectorHit, topK int) []MemoryVectorHit {
	if topK <= 0 {
		topK = defaultMemoryVectorTopK
	}

	merged := make(map[string]MemoryVectorHit, len(a)+len(b))

	add := func(hit MemoryVectorHit) {
		hit.Source = strings.TrimSpace(hit.Source)
		hit.Text = strings.TrimSpace(hit.Text)
		if hit.Source == "" || hit.Text == "" {
			return
		}
		if existing, ok := merged[hit.Source]; ok {
			// Keep the higher-scoring hit; if scores tie, prefer the longer snippet.
			if hit.Score > existing.Score || (hit.Score == existing.Score && len(hit.Text) > len(existing.Text)) {
				merged[hit.Source] = hit
			}
			return
		}
		merged[hit.Source] = hit
	}

	for _, hit := range a {
		add(hit)
	}
	for _, hit := range b {
		add(hit)
	}

	out := make([]MemoryVectorHit, 0, len(merged))
	for _, hit := range merged {
		out = append(out, hit)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			return out[i].Source < out[j].Source
		}
		return out[i].Score > out[j].Score
	})
	if len(out) > topK {
		out = out[:topK]
	}
	return out
}

func mergeHybridMemoryHits(ftsHits, vecHits []MemoryVectorHit, topK int, hybrid MemoryHybridSettings) []MemoryVectorHit {
	if topK <= 0 {
		topK = defaultMemoryVectorTopK
	}
	hybrid = normalizeMemoryHybridSettings(hybrid)

	merged := make(map[string]MemoryVectorHit, len(ftsHits)+len(vecHits))

	add := func(hit MemoryVectorHit) {
		hit.Source = strings.TrimSpace(hit.Source)
		hit.Text = strings.TrimSpace(hit.Text)
		if hit.Source == "" || hit.Text == "" {
			return
		}

		if existing, ok := merged[hit.Source]; ok {
			// Merge signals.
			if hit.HasFTS && (!existing.HasFTS || hit.FTSScore > existing.FTSScore) {
				existing.HasFTS = true
				existing.FTSScore = hit.FTSScore
			}
			if hit.HasVector && (!existing.HasVector || hit.VectorScore > existing.VectorScore) {
				existing.HasVector = true
				existing.VectorScore = hit.VectorScore
			}
			// Prefer longer snippet when both refer to same source.
			if len(hit.Text) > len(existing.Text) {
				existing.Text = hit.Text
			}
			merged[hit.Source] = existing
			return
		}

		merged[hit.Source] = hit
	}

	for _, hit := range ftsHits {
		if !hit.HasFTS {
			hit.HasFTS = true
			hit.FTSScore = hit.Score
		}
		hit.MatchKind = "fts"
		add(hit)
	}
	for _, hit := range vecHits {
		if !hit.HasVector {
			hit.HasVector = true
			hit.VectorScore = hit.Score
		}
		hit.MatchKind = "vector"
		add(hit)
	}

	out := make([]MemoryVectorHit, 0, len(merged))
	for _, hit := range merged {
		// Re-score deterministically.
		switch {
		case hit.HasFTS && hit.HasVector:
			hit.MatchKind = "hybrid"
			hit.Score = hybrid.FTSWeight*hit.FTSScore + hybrid.VectorWeight*hit.VectorScore
		case hit.HasFTS:
			hit.MatchKind = "fts"
			hit.Score = hit.FTSScore
		case hit.HasVector:
			hit.MatchKind = "vector"
			hit.Score = hit.VectorScore
		default:
			continue
		}
		out = append(out, hit)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			return out[i].Source < out[j].Source
		}
		return out[i].Score > out[j].Score
	})

	if len(out) > topK {
		out = out[:topK]
	}
	return out
}

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

type memoryBlockSpec struct {
	Label    string
	Limit    int  // max characters (runes) allowed for the block content
	ReadOnly bool // true if agent updates must not modify this block
}

var memoryBlockSpecs = []memoryBlockSpec{
	{Label: "persona", Limit: 2400, ReadOnly: true},
	{Label: "human", Limit: 3600, ReadOnly: true},
	{Label: "projects", Limit: 6000, ReadOnly: false},
	{Label: "facts", Limit: 9000, ReadOnly: false},
}

func lookupMemoryBlockSpec(label string) (memoryBlockSpec, bool) {
	label = strings.ToLower(strings.TrimSpace(label))
	if label == "" {
		return memoryBlockSpec{}, false
	}
	for _, spec := range memoryBlockSpecs {
		if spec.Label == label {
			return spec, true
		}
	}
	return memoryBlockSpec{}, false
}

func memoryBlockLabelForLegacySection(section string) (string, bool) {
	section = strings.TrimSpace(section)
	switch section {
	case "Profile":
		return "human", true
	case "Long-term Facts", "Constraints":
		return "facts", true
	case "Active Goals", "Open Threads", "Deprecated/Resolved":
		return "projects", true
	default:
		return "", false
	}
}

func memoryBlockLabels() []string {
	out := make([]string, 0, len(memoryBlockSpecs))
	for _, spec := range memoryBlockSpecs {
		out = append(out, spec.Label)
	}
	return out
}

func parseMemoryBlocks(content string) (map[string]string, bool) {
	blocks := map[string]string{}
	lines := strings.Split(content, "\n")
	current := ""
	found := false
	var buf []string

	flush := func() {
		if current == "" {
			buf = buf[:0]
			return
		}
		blocks[current] = strings.TrimSpace(strings.Join(buf, "\n"))
		buf = buf[:0]
	}

	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "##") && !strings.HasPrefix(line, "###") {
			heading := strings.TrimSpace(strings.TrimLeft(line, "#"))
			label := strings.ToLower(strings.TrimSpace(heading))
			if _, ok := lookupMemoryBlockSpec(label); ok {
				flush()
				current = label
				found = true
				continue
			}
		}
		if current != "" {
			buf = append(buf, raw)
		}
	}
	flush()
	return blocks, found
}

func renderMemoryBlocks(blocks map[string]string) string {
	now := time.Now().Format("2006-01-02 15:04")

	var sb strings.Builder
	sb.WriteString("# MEMORY\n\n")
	sb.WriteString("_Last organized: ")
	sb.WriteString(now)
	sb.WriteString("_\n\n")

	for _, label := range memoryBlockLabels() {
		sb.WriteString("## ")
		sb.WriteString(label)
		sb.WriteString("\n")
		if v := strings.TrimSpace(blocks[label]); v != "" {
			sb.WriteString(v)
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	return strings.TrimSpace(sb.String()) + "\n"
}

func sanitizeMemoryText(s string) string {
	// Remove NUL bytes and other non-text junk that can break JSON/SQLite.
	return strings.Map(func(r rune) rune {
		if r == 0 {
			return -1
		}
		return r
	}, s)
}

func extractBlockEntries(content string) []string {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil
	}

	lines := strings.Split(content, "\n")
	out := make([]string, 0, len(lines))
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimLeft(line, "-*+"))
		line = compactWhitespace(line)
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	return out
}

func mergeBlockEntries(base, incoming string) []string {
	baseEntries := extractBlockEntries(base)
	inEntries := extractBlockEntries(incoming)

	seen := make(map[string]struct{}, len(baseEntries)+len(inEntries))
	out := make([]string, 0, len(baseEntries)+len(inEntries))

	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		key := strings.ToLower(s)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, s)
	}

	for _, e := range baseEntries {
		add(e)
	}
	for _, e := range inEntries {
		add(e)
	}
	return out
}

func clipEntriesToLimit(entries []string, limit int) []string {
	if limit <= 0 || len(entries) == 0 {
		return entries
	}

	// Keep newest entries when trimming (tail-preserving).
	selectedRev := make([]string, 0, len(entries))
	used := 0
	for i := len(entries) - 1; i >= 0; i-- {
		entry := strings.TrimSpace(entries[i])
		if entry == "" {
			continue
		}
		// "- " + entry + "\n"
		entryLen := utf8.RuneCountInString(entry) + 3
		if used+entryLen > limit {
			if len(selectedRev) == 0 {
				// Single entry too large: truncate it to fit.
				max := maxInt(1, limit-3)
				selectedRev = append(selectedRev, truncateRunes(entry, max))
			}
			break
		}
		selectedRev = append(selectedRev, entry)
		used += entryLen
	}

	// Reverse back to chronological order.
	for i, j := 0, len(selectedRev)-1; i < j; i, j = i+1, j-1 {
		selectedRev[i], selectedRev[j] = selectedRev[j], selectedRev[i]
	}
	return selectedRev
}

func renderEntries(entries []string) string {
	var sb strings.Builder
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		sb.WriteString("- ")
		sb.WriteString(e)
		sb.WriteString("\n")
	}
	return strings.TrimSpace(sb.String())
}

func truncateRunes(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	out := make([]rune, 0, limit)
	for _, r := range s {
		if len(out) >= limit {
			break
		}
		out = append(out, r)
	}
	return strings.TrimSpace(string(out))
}

func parseMemoryAsBlocks(content string) map[string]string {
	content = strings.TrimSpace(content)
	if content == "" {
		return map[string]string{}
	}

	if blocks, ok := parseMemoryBlocks(content); ok {
		// Ensure all block keys exist for deterministic rendering.
		for _, label := range memoryBlockLabels() {
			if _, exists := blocks[label]; !exists {
				blocks[label] = ""
			}
		}
		return blocks
	}

	// Legacy format: map older sections into modern blocks.
	sections := parseMemorySections(content)
	projects := make([]string, 0, len(sections["Active Goals"])+len(sections["Open Threads"])+len(sections["Deprecated/Resolved"]))
	projects = append(projects, sections["Active Goals"]...)
	projects = append(projects, sections["Open Threads"]...)
	for _, entry := range sections["Deprecated/Resolved"] {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		projects = append(projects, "resolved: "+entry)
	}

	facts := make([]string, 0, len(sections["Long-term Facts"])+len(sections["Constraints"]))
	facts = append(facts, sections["Long-term Facts"]...)
	facts = append(facts, sections["Constraints"]...)

	blocks := map[string]string{
		"persona":  "",
		"human":    renderEntries(sections["Profile"]),
		"projects": renderEntries(projects),
		"facts":    renderEntries(facts),
	}
	// Preserve anything else by appending to facts (best-effort).
	for section, entries := range sections {
		switch section {
		case "Profile", "Active Goals", "Open Threads", "Long-term Facts", "Constraints", "Deprecated/Resolved":
			continue
		default:
			if len(entries) == 0 {
				continue
			}
			extra := renderEntries(entries)
			if extra == "" {
				continue
			}
			if blocks["facts"] != "" {
				blocks["facts"] += "\n" + extra
			} else {
				blocks["facts"] = extra
			}
		}
	}
	return blocks
}

// MemorySearchTool performs semantic lookup over persisted memory files.
type MemorySearchTool struct {
	memoryProvider  func(ctx context.Context) MemoryReader
	defaultTopK     int
	defaultMinScore float64
}

func NewMemorySearchTool(memory MemoryReader, defaultTopK int, defaultMinScore float64) *MemorySearchTool {
	return NewMemorySearchToolWithProvider(func(context.Context) MemoryReader { return memory }, defaultTopK, defaultMinScore)
}

func NewMemorySearchToolWithProvider(provider func(ctx context.Context) MemoryReader, defaultTopK int, defaultMinScore float64) *MemorySearchTool {
	if defaultTopK <= 0 {
		defaultTopK = defaultMemoryVectorTopK
	}
	if defaultMinScore < 0 || defaultMinScore >= 1 {
		defaultMinScore = defaultMemoryVectorMinScore
	}
	return &MemorySearchTool{
		memoryProvider:  provider,
		defaultTopK:     defaultTopK,
		defaultMinScore: defaultMinScore,
	}
}

func (t *MemorySearchTool) Name() string {
	return "memory_search"
}

func (t *MemorySearchTool) ParallelPolicy() tools.ToolParallelPolicy {
	return tools.ToolParallelReadOnly
}

func (t *MemorySearchTool) Description() string {
	return "Semantically search MEMORY.md and recent daily notes for relevant facts. Returns structured JSON hits for stable LLM consumption."
}

func (t *MemorySearchTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Natural-language query to search semantic memory",
			},
			"top_k": map[string]any{
				"type":        "integer",
				"description": "Maximum number of hits to return (default from agent settings)",
			},
			"min_score": map[string]any{
				"type":        "number",
				"description": "Minimum cosine similarity in [0,1), lower means broader recall",
			},
		},
		"required": []string{"query"},
	}
}

func (t *MemorySearchTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	memory := (MemoryReader)(nil)
	if t.memoryProvider != nil {
		memory = t.memoryProvider(ctx)
	}
	if memory == nil {
		return tools.ErrorResult("memory store unavailable")
	}

	query, ok := args["query"].(string)
	if !ok || strings.TrimSpace(query) == "" {
		return tools.ErrorResult("query is required")
	}

	topK := t.defaultTopK
	if raw, ok := args["top_k"]; ok {
		switch v := raw.(type) {
		case int:
			if v > 0 {
				topK = v
			}
		case int64:
			if v > 0 {
				topK = int(v)
			}
		case float64:
			if int(v) > 0 {
				topK = int(v)
			}
		}
	}

	minScore := t.defaultMinScore
	if raw, ok := args["min_score"]; ok {
		if v, ok := raw.(float64); ok && v >= 0 && v < 1 {
			minScore = v
		}
	}

	hits, err := memory.SearchRelevant(ctx, query, topK, minScore)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("memory search failed: %v", err)).WithError(err)
	}

	type memoryHit struct {
		ID         string             `json:"id"`
		Score      float64            `json:"score"`
		MatchKind  string             `json:"match_kind,omitempty"`
		Signals    map[string]float64 `json:"signals,omitempty"`
		Snippet    string             `json:"snippet"`
		Source     string             `json:"source"`
		SourcePath string             `json:"source_path"`
		Tags       []string           `json:"tags"`
	}
	type memorySearchResult struct {
		Kind     string      `json:"kind"`
		Query    string      `json:"query"`
		TopK     int         `json:"top_k"`
		MinScore float64     `json:"min_score"`
		Hits     []memoryHit `json:"hits"`
	}

	result := memorySearchResult{
		Kind:     "memory_search_result",
		Query:    query,
		TopK:     topK,
		MinScore: minScore,
		Hits:     make([]memoryHit, 0, len(hits)),
	}

	for _, hit := range hits {
		sourcePath := hit.Source
		if before, _, ok := strings.Cut(hit.Source, "#"); ok && strings.TrimSpace(before) != "" {
			sourcePath = before
		}
		result.Hits = append(result.Hits, memoryHit{
			ID:         hit.Source,
			Score:      hit.Score,
			MatchKind:  strings.TrimSpace(hit.MatchKind),
			Signals:    buildMemoryHitSignals(hit),
			Snippet:    utils.Truncate(hit.Text, 240),
			Source:     hit.Source,
			SourcePath: sourcePath,
			Tags:       []string{},
		})
	}

	payload, err := json.Marshal(result)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("memory search failed: %v", err)).WithError(err)
	}

	// Keep a human-readable summary (for traces / debugging) while returning JSON to the LLM.
	var summary strings.Builder
	if len(hits) == 0 {
		summary.WriteString("No relevant memory hits found.")
	} else {
		summary.WriteString("Memory search hits:\n")
		for _, hit := range hits {
			summary.WriteString(fmt.Sprintf("- (score=%.2f, source=%s) %s\n", hit.Score, hit.Source, hit.Text))
		}
	}

	return &tools.ToolResult{
		ForLLM:  string(payload),
		ForUser: strings.TrimSpace(summary.String()),
		Silent:  true,
		IsError: false,
		Async:   false,
	}
}

func buildMemoryHitSignals(hit MemoryVectorHit) map[string]float64 {
	signals := map[string]float64{}
	if hit.HasFTS {
		signals["fts_score"] = hit.FTSScore
	}
	if hit.HasVector {
		signals["vector_score"] = hit.VectorScore
	}
	if len(signals) == 0 {
		return nil
	}
	return signals
}

// MemoryGetTool returns a specific memory item by its source citation.
type MemoryGetTool struct {
	memoryProvider func(ctx context.Context) MemoryReader
}

func NewMemoryGetTool(memory MemoryReader) *MemoryGetTool {
	return NewMemoryGetToolWithProvider(func(context.Context) MemoryReader { return memory })
}

func NewMemoryGetToolWithProvider(provider func(ctx context.Context) MemoryReader) *MemoryGetTool {
	return &MemoryGetTool{memoryProvider: provider}
}

func (t *MemoryGetTool) Name() string {
	return "memory_get"
}

func (t *MemoryGetTool) ParallelPolicy() tools.ToolParallelPolicy {
	return tools.ToolParallelReadOnly
}

func (t *MemoryGetTool) Description() string {
	return "Retrieve one memory entry by source citation returned from memory_search. Returns structured JSON for stable LLM consumption."
}

func (t *MemoryGetTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"source": map[string]any{
				"type":        "string",
				"description": "Citation source like MEMORY.md#facts (also accepts legacy sections like MEMORY.md#Long-term Facts)",
			},
		},
		"required": []string{"source"},
	}
}

func (t *MemoryGetTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	memory := (MemoryReader)(nil)
	if t.memoryProvider != nil {
		memory = t.memoryProvider(ctx)
	}
	if memory == nil {
		return tools.ErrorResult("memory store unavailable")
	}

	source, ok := args["source"].(string)
	if !ok || strings.TrimSpace(source) == "" {
		return tools.ErrorResult("source is required")
	}

	hit, found, err := memory.GetBySource(ctx, source)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("memory get failed: %v", err)).WithError(err)
	}

	type memoryGetResult struct {
		Kind  string `json:"kind"`
		Found bool   `json:"found"`
		Hit   struct {
			ID         string   `json:"id,omitempty"`
			Source     string   `json:"source,omitempty"`
			SourcePath string   `json:"source_path,omitempty"`
			Content    string   `json:"content,omitempty"`
			Tags       []string `json:"tags,omitempty"`
		} `json:"hit"`
	}

	result := memoryGetResult{
		Kind:  "memory_get_result",
		Found: found,
	}
	if found {
		sourcePath := hit.Source
		if before, _, ok := strings.Cut(hit.Source, "#"); ok && strings.TrimSpace(before) != "" {
			sourcePath = before
		}

		result.Hit.ID = hit.Source
		result.Hit.Source = hit.Source
		result.Hit.SourcePath = sourcePath
		result.Hit.Content = hit.Text
		result.Hit.Tags = []string{}
	}

	payload, err := json.Marshal(result)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("memory get failed: %v", err)).WithError(err)
	}

	userSummary := "Memory source not found."
	if found {
		userSummary = fmt.Sprintf("Memory entry:\n- source=%s\n- content=%s", hit.Source, hit.Text)
	}

	return &tools.ToolResult{
		ForLLM:  string(payload),
		ForUser: userSummary,
		Silent:  true,
		IsError: false,
		Async:   false,
	}
}

const (
	defaultMemoryVectorDimensions      = 256
	defaultMemoryVectorTopK            = 6
	defaultMemoryVectorMinScore        = 0.15
	defaultMemoryVectorMaxContextChars = 1800
	defaultMemoryVectorRecentDailyDays = 14
	memoryVectorChunkChars             = 280
)

// MemoryVectorSettings controls semantic memory indexing and retrieval behavior.
type MemoryVectorSettings struct {
	Enabled         bool
	Dimensions      int
	TopK            int
	MinScore        float64
	MaxContextChars int
	RecentDailyDays int
	Embedding       MemoryVectorEmbeddingSettings
	Hybrid          MemoryHybridSettings
}

type MemoryVectorHit struct {
	Source string
	Text   string
	Score  float64

	// MatchKind indicates which retriever produced this hit:
	// - "fts": SQLite FTS keyword match
	// - "vector": semantic vector search
	// - "hybrid": both signals available
	MatchKind string

	HasFTS      bool
	FTSScore    float64
	HasVector   bool
	VectorScore float64
}

type MemoryHybridSettings struct {
	FTSWeight    float64
	VectorWeight float64
}

type memoryVectorDocument struct {
	ID     string    `json:"id"`
	Source string    `json:"source"`
	Text   string    `json:"text"`
	Vector []float32 `json:"vector"`
}

type memoryVectorIndex struct {
	Version     int                    `json:"version"`
	BuiltAt     string                 `json:"built_at"`
	Fingerprint string                 `json:"fingerprint"`
	EmbedderSig string                 `json:"embedder_signature,omitempty"`
	Dimensions  int                    `json:"dimensions"`
	Documents   []memoryVectorDocument `json:"documents"`
}

type memoryVectorSourceFile struct {
	Path    string
	RelPath string
	Size    int64
	ModUnix int64
}

type memoryVectorStore struct {
	memoryDir  string
	memoryFile string
	indexPath  string

	mu       sync.Mutex
	settings MemoryVectorSettings
	embedder memoryVectorEmbedder
	cache    *memoryVectorIndex
}

func defaultMemoryVectorSettings() MemoryVectorSettings {
	return MemoryVectorSettings{
		Enabled:         true,
		Dimensions:      defaultMemoryVectorDimensions,
		TopK:            defaultMemoryVectorTopK,
		MinScore:        defaultMemoryVectorMinScore,
		MaxContextChars: defaultMemoryVectorMaxContextChars,
		RecentDailyDays: defaultMemoryVectorRecentDailyDays,
		Hybrid: MemoryHybridSettings{
			FTSWeight:    0.6,
			VectorWeight: 0.4,
		},
	}
}

func normalizeMemoryVectorSettings(settings MemoryVectorSettings) MemoryVectorSettings {
	if settings.Dimensions <= 0 {
		settings.Dimensions = defaultMemoryVectorDimensions
	}
	if settings.TopK <= 0 {
		settings.TopK = defaultMemoryVectorTopK
	}
	if settings.MinScore < 0 || settings.MinScore >= 1 {
		settings.MinScore = defaultMemoryVectorMinScore
	}
	if settings.MaxContextChars <= 0 {
		settings.MaxContextChars = defaultMemoryVectorMaxContextChars
	}
	if settings.RecentDailyDays <= 0 {
		settings.RecentDailyDays = defaultMemoryVectorRecentDailyDays
	}
	settings.Embedding = normalizeMemoryVectorEmbeddingSettings(settings.Embedding)
	settings.Hybrid = normalizeMemoryHybridSettings(settings.Hybrid)
	return settings
}

func normalizeMemoryHybridSettings(settings MemoryHybridSettings) MemoryHybridSettings {
	if settings.FTSWeight < 0 || settings.FTSWeight > 1 {
		settings.FTSWeight = 0
	}
	if settings.VectorWeight < 0 || settings.VectorWeight > 1 {
		settings.VectorWeight = 0
	}
	if settings.FTSWeight == 0 && settings.VectorWeight == 0 {
		settings.FTSWeight = 0.6
		settings.VectorWeight = 0.4
	}
	sum := settings.FTSWeight + settings.VectorWeight
	if sum > 0 {
		settings.FTSWeight /= sum
		settings.VectorWeight /= sum
	}
	return settings
}

func newMemoryVectorStore(memoryDir, memoryFile string, settings MemoryVectorSettings) *memoryVectorStore {
	settings = normalizeMemoryVectorSettings(settings)
	embedder := buildMemoryVectorEmbedder(settings.Embedding, settings.Dimensions)
	return &memoryVectorStore{
		memoryDir:  memoryDir,
		memoryFile: memoryFile,
		indexPath:  filepath.Join(memoryDir, "vector", "index.json"),
		settings:   settings,
		embedder:   embedder,
	}
}

func (vs *memoryVectorStore) SetSettings(settings MemoryVectorSettings) {
	vs.mu.Lock()
	defer vs.mu.Unlock()

	prevSettings := vs.settings
	oldSig := ""
	if vs.embedder != nil {
		oldSig = vs.embedder.Signature()
	}

	normalized := normalizeMemoryVectorSettings(settings)
	newEmbedder := buildMemoryVectorEmbedder(normalized.Embedding, normalized.Dimensions)
	newSig := ""
	if newEmbedder != nil {
		newSig = newEmbedder.Signature()
	}

	if prevSettings != normalized || oldSig != newSig {
		vs.cache = nil
	}

	vs.settings = normalized
	vs.embedder = newEmbedder
}

func (vs *memoryVectorStore) MarkDirty() {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	vs.cache = nil
}

func (vs *memoryVectorStore) Rebuild(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	vs.mu.Lock()
	defer vs.mu.Unlock()
	return vs.rebuildLocked(ctx)
}

func (vs *memoryVectorStore) Search(ctx context.Context, query string, topK int, minScore float64) ([]MemoryVectorHit, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	vs.mu.Lock()
	defer vs.mu.Unlock()

	if !vs.settings.Enabled {
		return nil, nil
	}
	if topK <= 0 {
		topK = vs.settings.TopK
	}
	if minScore < 0 {
		minScore = 0
	}

	if err := vs.ensureIndexLocked(ctx); err != nil {
		return nil, err
	}
	if vs.cache == nil || len(vs.cache.Documents) == 0 {
		return nil, nil
	}

	if vs.embedder == nil {
		return nil, fmt.Errorf("memory embedder not configured")
	}
	queryVecs, err := vs.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	if len(queryVecs) == 0 {
		return nil, nil
	}
	queryVec := queryVecs[0]
	queryTerms := uniqueTokenSet(tokenizeForEmbedding(query))
	if len(queryVec) == 0 {
		return nil, nil
	}

	hits := make([]MemoryVectorHit, 0, minInt(topK, len(vs.cache.Documents)))
	for _, doc := range vs.cache.Documents {
		vectorScore := cosineSimilarity(queryVec, doc.Vector)
		keywordScore := lexicalSimilarity(queryTerms, tokenizeForEmbedding(doc.Text))
		// Blend semantic and lexical signals to improve recall on terse notes and identifiers.
		score := 0.8*vectorScore + 0.2*keywordScore
		if score < minScore {
			continue
		}
		hits = append(hits, MemoryVectorHit{
			Source:      doc.Source,
			Text:        doc.Text,
			Score:       score,
			MatchKind:   "vector",
			HasVector:   true,
			VectorScore: score,
		})
	}

	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score == hits[j].Score {
			return hits[i].Source < hits[j].Source
		}
		return hits[i].Score > hits[j].Score
	})

	if len(hits) > topK {
		hits = hits[:topK]
	}
	return hits, nil
}

func (vs *memoryVectorStore) GetBySource(ctx context.Context, source string) (MemoryVectorHit, bool, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return MemoryVectorHit{}, false, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	vs.mu.Lock()
	defer vs.mu.Unlock()

	if !vs.settings.Enabled {
		return MemoryVectorHit{}, false, nil
	}

	if err := vs.ensureIndexLocked(ctx); err != nil {
		return MemoryVectorHit{}, false, err
	}
	if vs.cache == nil || len(vs.cache.Documents) == 0 {
		return MemoryVectorHit{}, false, nil
	}

	for _, doc := range vs.cache.Documents {
		if doc.Source != source {
			continue
		}
		return MemoryVectorHit{
			Source: doc.Source,
			Text:   doc.Text,
			Score:  1,
		}, true, nil
	}

	return MemoryVectorHit{}, false, nil
}

func (vs *memoryVectorStore) ensureIndexLocked(ctx context.Context) error {
	sources, fingerprint, err := vs.collectSourceFilesLocked(time.Now())
	if err != nil {
		return err
	}

	if vs.cache != nil && vs.cache.Fingerprint == fingerprint {
		return nil
	}

	if disk, loadErr := vs.loadIndexLocked(); loadErr == nil && disk != nil {
		if disk.Fingerprint == fingerprint {
			vs.cache = disk
			return nil
		}
	}

	return vs.rebuildFromSourcesLocked(ctx, sources, fingerprint)
}

func (vs *memoryVectorStore) rebuildLocked(ctx context.Context) error {
	sources, fingerprint, err := vs.collectSourceFilesLocked(time.Now())
	if err != nil {
		return err
	}
	return vs.rebuildFromSourcesLocked(ctx, sources, fingerprint)
}

func (vs *memoryVectorStore) rebuildFromSourcesLocked(
	ctx context.Context,
	sources []memoryVectorSourceFile,
	fingerprint string,
) error {
	if ctx == nil {
		ctx = context.Background()
	}

	docs, err := vs.buildDocumentsLocked(sources)
	if err != nil {
		return err
	}

	// Attempt to reuse existing embeddings when the embedder signature matches.
	var reuse map[string][]float32
	if disk, loadErr := vs.loadIndexLocked(); loadErr == nil && disk != nil {
		wantSig := ""
		if vs.embedder != nil {
			wantSig = vs.embedder.Signature()
		}
		if strings.TrimSpace(wantSig) != "" && disk.EmbedderSig == wantSig && len(disk.Documents) > 0 {
			reuse = make(map[string][]float32, len(disk.Documents))
			for _, doc := range disk.Documents {
				if doc.ID == "" || len(doc.Vector) == 0 {
					continue
				}
				reuse[doc.ID] = doc.Vector
			}
		}
	}

	if vs.embedder == nil {
		return fmt.Errorf("memory embedder not configured")
	}

	toEmbed := make([]string, 0, len(docs))
	embedIdx := make([]int, 0, len(docs))
	for i := range docs {
		if reuse != nil {
			if vec, ok := reuse[docs[i].ID]; ok && len(vec) > 0 {
				docs[i].Vector = vec
				continue
			}
		}
		toEmbed = append(toEmbed, docs[i].Text)
		embedIdx = append(embedIdx, i)
	}

	if len(toEmbed) > 0 {
		vecs, err := vs.embedder.Embed(ctx, toEmbed)
		if err != nil {
			return err
		}
		if len(vecs) != len(toEmbed) {
			return fmt.Errorf("embedding backend returned %d vectors for %d inputs", len(vecs), len(toEmbed))
		}
		for j, vec := range vecs {
			docs[embedIdx[j]].Vector = vec
		}
	}

	dims := 0
	for _, doc := range docs {
		if len(doc.Vector) > 0 {
			dims = len(doc.Vector)
			break
		}
	}

	embedderSig := ""
	if vs.embedder != nil {
		embedderSig = vs.embedder.Signature()
	}

	index := &memoryVectorIndex{
		Version:     2,
		BuiltAt:     time.Now().Format(time.RFC3339),
		Fingerprint: fingerprint,
		EmbedderSig: embedderSig,
		Dimensions:  dims,
		Documents:   docs,
	}
	if err := vs.saveIndexLocked(index); err != nil {
		return err
	}
	vs.cache = index
	return nil
}

func (vs *memoryVectorStore) collectSourceFilesLocked(now time.Time) ([]memoryVectorSourceFile, string, error) {
	sources := make([]memoryVectorSourceFile, 0, vs.settings.RecentDailyDays+1)

	if info, err := os.Stat(vs.memoryFile); err == nil && !info.IsDir() {
		sources = append(sources, memoryVectorSourceFile{
			Path:    vs.memoryFile,
			RelPath: "MEMORY.md",
			Size:    info.Size(),
			ModUnix: info.ModTime().Unix(),
		})
	} else if err != nil && !os.IsNotExist(err) {
		return nil, "", err
	}

	for i := 0; i < vs.settings.RecentDailyDays; i++ {
		day := now.AddDate(0, 0, -i).Format("20060102")
		candidate := filepath.Join(vs.memoryDir, day[:6], day+".md")

		info, err := os.Stat(candidate)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, "", err
		}
		if info.IsDir() {
			continue
		}

		rel, relErr := filepath.Rel(vs.memoryDir, candidate)
		if relErr != nil {
			rel = filepath.Base(candidate)
		}
		sources = append(sources, memoryVectorSourceFile{
			Path:    candidate,
			RelPath: filepath.ToSlash(rel),
			Size:    info.Size(),
			ModUnix: info.ModTime().Unix(),
		})
	}

	embedderSig := ""
	if vs.embedder != nil {
		embedderSig = vs.embedder.Signature()
	}
	fingerprint := buildSourceFingerprint(sources, embedderSig)
	return sources, fingerprint, nil
}

func buildSourceFingerprint(sources []memoryVectorSourceFile, embedderSig string) string {
	h := sha1.New()
	fmt.Fprintf(h, "embedder=%s\n", strings.TrimSpace(embedderSig))
	for _, src := range sources {
		fmt.Fprintf(h, "%s|%d|%d\n", src.RelPath, src.Size, src.ModUnix)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (vs *memoryVectorStore) buildDocumentsLocked(sources []memoryVectorSourceFile) ([]memoryVectorDocument, error) {
	docs := make([]memoryVectorDocument, 0, len(sources)*8)

	for _, src := range sources {
		data, err := os.ReadFile(src.Path)
		if err != nil {
			return nil, err
		}
		content := string(data)
		if strings.TrimSpace(content) == "" {
			continue
		}

		if src.RelPath == "MEMORY.md" {
			blocks := parseMemoryAsBlocks(content)
			for _, label := range memoryBlockLabels() {
				for _, entry := range extractBlockEntries(blocks[label]) {
					text := compactWhitespace(entry)
					if text == "" {
						continue
					}
					payload := label + ": " + text
					docs = append(docs, memoryVectorDocument{
						ID:     buildMemoryVectorID(src.RelPath, label, text),
						Source: fmt.Sprintf("%s#%s", src.RelPath, label),
						Text:   payload,
					})
				}
			}
			continue
		}

		chunks := chunkMarkdownForVectors(content, memoryVectorChunkChars)
		for idx, chunk := range chunks {
			if strings.TrimSpace(chunk) == "" {
				continue
			}
			docs = append(docs, memoryVectorDocument{
				ID:     buildMemoryVectorID(src.RelPath, fmt.Sprintf("%d", idx+1), chunk),
				Source: fmt.Sprintf("%s#%d", src.RelPath, idx+1),
				Text:   chunk,
			})
		}
	}

	return docs, nil
}

func chunkMarkdownForVectors(content string, maxChars int) []string {
	if maxChars <= 0 {
		maxChars = memoryVectorChunkChars
	}

	lines := strings.Split(content, "\n")
	out := make([]string, 0, 8)
	var current strings.Builder
	currentHeading := ""

	flush := func() {
		chunk := compactWhitespace(current.String())
		if chunk != "" {
			out = append(out, chunk)
		}
		current.Reset()
	}

	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || line == "---" {
			flush()
			continue
		}
		if strings.HasPrefix(line, "#") {
			flush()
			currentHeading = strings.TrimSpace(strings.TrimLeft(line, "#"))
			continue
		}

		line = strings.TrimSpace(strings.TrimLeft(line, "-*+"))
		if line == "" {
			continue
		}
		if currentHeading != "" {
			line = currentHeading + ": " + line
		}

		if current.Len() == 0 {
			current.WriteString(line)
		} else {
			if current.Len()+1+len(line) > maxChars {
				flush()
				current.WriteString(line)
			} else {
				current.WriteString(" ")
				current.WriteString(line)
			}
		}
	}
	flush()

	return out
}

func (vs *memoryVectorStore) loadIndexLocked() (*memoryVectorIndex, error) {
	data, err := os.ReadFile(vs.indexPath)
	if err != nil {
		return nil, err
	}
	var index memoryVectorIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, err
	}
	return &index, nil
}

func (vs *memoryVectorStore) saveIndexLocked(index *memoryVectorIndex) error {
	if err := os.MkdirAll(filepath.Dir(vs.indexPath), 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(vs.indexPath, payload, 0o644)
}

func buildMemoryVectorID(parts ...string) string {
	h := sha1.New()
	for _, part := range parts {
		if part == "" {
			continue
		}
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func embedHashedText(text string, dims int) []float32 {
	if dims <= 0 {
		return nil
	}

	tokens := tokenizeForEmbedding(text)
	if len(tokens) == 0 {
		return nil
	}

	tf := make(map[string]int, len(tokens))
	for _, token := range tokens {
		tf[token]++
	}

	vec := make([]float64, dims)
	for token, count := range tf {
		h := fnv.New32a()
		_, _ = h.Write([]byte(token))
		sum := h.Sum32()

		index := int(sum % uint32(dims))
		sign := 1.0
		if (sum>>31)&1 == 1 {
			sign = -1
		}

		weight := 1.0 + math.Log(float64(count))
		vec[index] += sign * weight
	}

	norm := 0.0
	for _, v := range vec {
		norm += v * v
	}
	if norm == 0 {
		return nil
	}
	norm = math.Sqrt(norm)

	out := make([]float32, dims)
	for i, v := range vec {
		out[i] = float32(v / norm)
	}
	return out
}

func tokenizeForEmbedding(text string) []string {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return nil
	}

	tokens := make([]string, 0, 32)
	var current []rune
	flush := func() {
		if len(current) == 0 {
			return
		}
		tokens = append(tokens, string(current))
		current = current[:0]
	}

	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			current = append(current, r)
			continue
		}
		flush()
	}
	flush()

	if len(tokens) > 0 {
		expanded := make([]string, 0, len(tokens)*3)
		for _, token := range tokens {
			expanded = append(expanded, token)
			runes := []rune(token)
			if len(runes) < 4 {
				continue
			}
			for i := 0; i+3 <= len(runes); i++ {
				expanded = append(expanded, string(runes[i:i+3]))
			}
		}
		return expanded
	}

	// For very short non-word strings, fall back to rune-level tokens.
	for _, r := range text {
		if unicode.IsSpace(r) {
			continue
		}
		tokens = append(tokens, string(r))
	}
	return tokens
}

func cosineSimilarity(a, b []float32) float64 {
	n := minInt(len(a), len(b))
	if n == 0 {
		return 0
	}

	dot := 0.0
	normA := 0.0
	normB := 0.0
	for i := 0; i < n; i++ {
		av := float64(a[i])
		bv := float64(b[i])
		dot += av * bv
		normA += av * av
		normB += bv * bv
	}

	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / math.Sqrt(normA*normB)
}

func compactWhitespace(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}

func uniqueTokenSet(tokens []string) map[string]struct{} {
	if len(tokens) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		if token == "" {
			continue
		}
		out[token] = struct{}{}
	}
	return out
}

func lexicalSimilarity(queryTokens map[string]struct{}, docTokens []string) float64 {
	if len(queryTokens) == 0 || len(docTokens) == 0 {
		return 0
	}

	docSet := uniqueTokenSet(docTokens)
	if len(docSet) == 0 {
		return 0
	}

	overlap := 0
	for token := range queryTokens {
		if _, ok := docSet[token]; ok {
			overlap++
		}
	}
	if overlap == 0 {
		return 0
	}

	denom := len(queryTokens)
	if denom == 0 {
		return 0
	}
	return float64(overlap) / float64(denom)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// MemoryVectorEmbeddingSettings configures how semantic vectors are generated for memory search.
//
// Default behavior is a fast local hashing embedder (no network). When Kind is set to
// "openai_compat", X-Claw will call an OpenAI-compatible embeddings endpoint.
type MemoryVectorEmbeddingSettings struct {
	Kind string

	APIKey  string
	APIBase string
	Model   string
	Proxy   string

	BatchSize             int
	RequestTimeoutSeconds int
}

func normalizeMemoryVectorEmbeddingSettings(s MemoryVectorEmbeddingSettings) MemoryVectorEmbeddingSettings {
	s.Kind = strings.ToLower(strings.TrimSpace(s.Kind))
	s.APIKey = strings.TrimSpace(s.APIKey)
	s.APIBase = strings.TrimRight(strings.TrimSpace(s.APIBase), "/")
	s.Model = strings.TrimSpace(s.Model)
	s.Proxy = strings.TrimSpace(s.Proxy)

	if s.BatchSize <= 0 {
		s.BatchSize = 64
	}
	if s.RequestTimeoutSeconds <= 0 {
		s.RequestTimeoutSeconds = 30
	}

	if s.Kind == "" {
		s.Kind = "hashed"
	}

	return s
}

type memoryVectorEmbedder interface {
	Kind() string
	// Signature returns a stable, non-secret identifier used for index fingerprinting.
	Signature() string
	Embed(ctx context.Context, inputs []string) ([][]float32, error)
}

type hashedMemoryVectorEmbedder struct {
	dims int
}

func (e *hashedMemoryVectorEmbedder) Kind() string { return "hashed" }

func (e *hashedMemoryVectorEmbedder) Signature() string {
	return fmt.Sprintf("hashed:dims=%d", e.dims)
}

func (e *hashedMemoryVectorEmbedder) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	_ = ctx
	if len(inputs) == 0 {
		return nil, nil
	}
	out := make([][]float32, 0, len(inputs))
	for _, input := range inputs {
		out = append(out, embedHashedText(input, e.dims))
	}
	return out, nil
}

type errorMemoryVectorEmbedder struct {
	kind string
	err  error
}

func (e *errorMemoryVectorEmbedder) Kind() string { return e.kind }

func (e *errorMemoryVectorEmbedder) Signature() string {
	if strings.TrimSpace(e.kind) == "" {
		return "error:unknown"
	}
	return "error:" + e.kind
}

func (e *errorMemoryVectorEmbedder) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	_ = ctx
	_ = inputs
	if e.err != nil {
		return nil, e.err
	}
	return nil, fmt.Errorf("embedding unavailable")
}

func buildMemoryVectorEmbedder(settings MemoryVectorEmbeddingSettings, dims int) memoryVectorEmbedder {
	settings = normalizeMemoryVectorEmbeddingSettings(settings)

	switch settings.Kind {
	case "hashed":
		return &hashedMemoryVectorEmbedder{dims: dims}
	case "openai_compat":
		if settings.APIBase == "" || settings.Model == "" {
			return &errorMemoryVectorEmbedder{
				kind: settings.Kind,
				err:  fmt.Errorf("embedding config incomplete: api_base and model are required"),
			}
		}
		return newOpenAICompatMemoryVectorEmbedder(settings)
	default:
		return &errorMemoryVectorEmbedder{
			kind: settings.Kind,
			err:  fmt.Errorf("unknown embedding kind %q", settings.Kind),
		}
	}
}

type openAICompatMemoryVectorEmbedder struct {
	apiKey    string
	apiBase   string
	model     string
	batchSize int
	timeout   time.Duration
	client    *http.Client
}

func newOpenAICompatMemoryVectorEmbedder(settings MemoryVectorEmbeddingSettings) memoryVectorEmbedder {
	settings = normalizeMemoryVectorEmbeddingSettings(settings)

	timeout := time.Duration(settings.RequestTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	client := &http.Client{Timeout: timeout}
	if settings.Proxy != "" {
		if parsed, err := url.Parse(settings.Proxy); err == nil {
			client.Transport = &http.Transport{
				Proxy: http.ProxyURL(parsed),
			}
		}
	}

	return &openAICompatMemoryVectorEmbedder{
		apiKey:    settings.APIKey,
		apiBase:   strings.TrimRight(settings.APIBase, "/"),
		model:     settings.Model,
		batchSize: settings.BatchSize,
		timeout:   timeout,
		client:    client,
	}
}

func (e *openAICompatMemoryVectorEmbedder) Kind() string { return "openai_compat" }

func (e *openAICompatMemoryVectorEmbedder) Signature() string {
	base := strings.TrimRight(strings.TrimSpace(e.apiBase), "/")
	model := strings.TrimSpace(e.model)
	return fmt.Sprintf("openai_compat:model=%s;base=%s", model, base)
}

func (e *openAICompatMemoryVectorEmbedder) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	if e == nil || e.client == nil {
		return nil, fmt.Errorf("embedding client not configured")
	}
	if strings.TrimSpace(e.apiBase) == "" {
		return nil, fmt.Errorf("embedding api_base not configured")
	}
	if strings.TrimSpace(e.model) == "" {
		return nil, fmt.Errorf("embedding model not configured")
	}

	batchSize := e.batchSize
	if batchSize <= 0 {
		batchSize = 64
	}

	out := make([][]float32, 0, len(inputs))
	for start := 0; start < len(inputs); start += batchSize {
		end := start + batchSize
		if end > len(inputs) {
			end = len(inputs)
		}

		vecs, err := e.embedBatch(ctx, inputs[start:end])
		if err != nil {
			return nil, err
		}
		out = append(out, vecs...)
	}
	return out, nil
}

func (e *openAICompatMemoryVectorEmbedder) embedBatch(ctx context.Context, inputs []string) ([][]float32, error) {
	type embeddingRequest struct {
		Model string   `json:"model"`
		Input []string `json:"input"`
	}
	reqBody := embeddingRequest{
		Model: e.model,
		Input: inputs,
	}
	raw, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal embeddings request: %w", err)
	}

	if _, ok := ctx.Deadline(); !ok && e.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.timeout)
		defer cancel()
	}

	endpoint := strings.TrimRight(e.apiBase, "/") + "/embeddings"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("create embeddings request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(e.apiKey) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(e.apiKey))
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embeddings request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read embeddings response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		msg := string(body)
		if len(msg) > 1200 {
			msg = msg[:1200] + "... (truncated)"
		}
		return nil, fmt.Errorf("embeddings API error: status=%d body=%s", resp.StatusCode, msg)
	}

	type embeddingItem struct {
		Embedding []float64 `json:"embedding"`
		Index     int       `json:"index"`
	}
	var parsed struct {
		Data []embeddingItem `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("unmarshal embeddings response: %w", err)
	}
	if len(parsed.Data) == 0 {
		return nil, fmt.Errorf("embeddings response missing data")
	}

	// Some providers may return items out of order. Preserve original input order via index.
	items := make([]embeddingItem, 0, len(parsed.Data))
	for _, item := range parsed.Data {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Index < items[j].Index })

	vecs := make([][]float32, len(inputs))
	for _, item := range items {
		if item.Index < 0 || item.Index >= len(vecs) {
			continue
		}
		if len(item.Embedding) == 0 {
			continue
		}
		out := make([]float32, len(item.Embedding))
		for i := range item.Embedding {
			out[i] = float32(item.Embedding[i])
		}
		vecs[item.Index] = out
	}

	for i := range vecs {
		if len(vecs[i]) == 0 {
			return nil, fmt.Errorf("embeddings response missing item for index %d", i)
		}
	}

	return vecs, nil
}

const memoryFTSDriver = "sqlite"

type memoryFTSStore struct {
	memoryDir  string
	memoryFile string
	indexPath  string

	mu              sync.Mutex
	settings        MemoryVectorSettings
	db              *sql.DB
	lastFingerprint string
}

func newMemoryFTSStore(memoryDir, memoryFile string, settings MemoryVectorSettings) *memoryFTSStore {
	settings = normalizeMemoryVectorSettings(settings)
	return &memoryFTSStore{
		memoryDir:  memoryDir,
		memoryFile: memoryFile,
		indexPath:  filepath.Join(memoryDir, "fts", "index.sqlite"),
		settings:   settings,
	}
}

func (fs *memoryFTSStore) SetSettings(settings MemoryVectorSettings) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	normalized := normalizeMemoryVectorSettings(settings)
	if fs.settings != normalized {
		fs.lastFingerprint = ""
	}
	fs.settings = normalized
}

func (fs *memoryFTSStore) MarkDirty() {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.lastFingerprint = ""
}

func (fs *memoryFTSStore) Search(ctx context.Context, query string, topK int) ([]MemoryVectorHit, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	if err := fs.ensureIndexLocked(ctx); err != nil {
		return nil, err
	}
	if fs.db == nil {
		return nil, fmt.Errorf("fts db unavailable")
	}

	if topK <= 0 {
		topK = defaultMemoryVectorTopK
	}

	match := buildFTSMatchQuery(query)
	if match == "" {
		return nil, nil
	}

	rows, err := fs.db.QueryContext(ctx,
		`SELECT source, text, bm25(docs) as rank FROM docs WHERE docs MATCH ? ORDER BY rank LIMIT ?`,
		match,
		topK,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]MemoryVectorHit, 0, topK)
	for rows.Next() {
		var source string
		var text string
		var rank float64
		if err := rows.Scan(&source, &text, &rank); err != nil {
			return nil, err
		}

		// Rank conversion: bm25() is not normalized and may be negative depending on SQLite build/config.
		// Convert to a stable [0,1] score for sorting/merging.
		r := math.Abs(rank)
		score := 1.0 / (1.0 + r)
		if score <= 0 {
			score = 0.01
		}
		if score > 0.999 {
			score = 0.999
		}

		out = append(out, MemoryVectorHit{
			Source:    strings.TrimSpace(source),
			Text:      strings.TrimSpace(text),
			Score:     score,
			MatchKind: "fts",
			HasFTS:    true,
			FTSScore:  score,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return out, nil
}

func (fs *memoryFTSStore) ensureIndexLocked(ctx context.Context) error {
	sources, fingerprint, err := fs.collectSourceFilesLocked()
	if err != nil {
		return err
	}

	if fs.lastFingerprint != "" && fs.lastFingerprint == fingerprint {
		return nil
	}

	db, err := fs.openDBLocked()
	if err != nil {
		return err
	}
	if db == nil {
		return fmt.Errorf("fts db not available")
	}

	if err := fs.ensureSchemaLocked(ctx); err != nil {
		return err
	}

	current, _ := fs.readMetaLocked(ctx, "fingerprint")
	if strings.TrimSpace(current) == fingerprint {
		fs.lastFingerprint = fingerprint
		return nil
	}

	if err := fs.rebuildLocked(ctx, sources, fingerprint); err != nil {
		return err
	}
	fs.lastFingerprint = fingerprint
	return nil
}

func (fs *memoryFTSStore) openDBLocked() (*sql.DB, error) {
	if fs.db != nil {
		return fs.db, nil
	}

	if err := os.MkdirAll(filepath.Dir(fs.indexPath), 0o755); err != nil {
		return nil, err
	}

	// modernc.org/sqlite uses "file:" DSN style (same as in whatsapp_native).
	dsn := "file:" + fs.indexPath + "?_foreign_keys=on"
	db, err := sql.Open(memoryFTSDriver, dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	// Best-effort pragmas for a local append/rebuild workload.
	if _, err := db.Exec("PRAGMA journal_mode = WAL"); err != nil {
		logger.DebugCF("memory", "fts pragma journal_mode failed (best-effort)", map[string]any{"error": err.Error()})
	}
	if _, err := db.Exec("PRAGMA synchronous = NORMAL"); err != nil {
		logger.DebugCF("memory", "fts pragma synchronous failed (best-effort)", map[string]any{"error": err.Error()})
	}

	fs.db = db
	return fs.db, nil
}

func (fs *memoryFTSStore) ensureSchemaLocked(ctx context.Context) error {
	if fs.db == nil {
		return fmt.Errorf("fts db unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// meta stores fingerprint and build info.
	if _, err := fs.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		return err
	}

	// docs is the FTS5 index of memory chunks/entries.
	// source is UNINDEXED so it can be returned but doesn't affect ranking.
	_, err := fs.db.ExecContext(ctx, `CREATE VIRTUAL TABLE IF NOT EXISTS docs USING fts5(source UNINDEXED, text, tokenize='unicode61')`)
	if err != nil {
		return err
	}

	return nil
}

func (fs *memoryFTSStore) readMetaLocked(ctx context.Context, key string) (string, error) {
	if fs.db == nil {
		return "", fmt.Errorf("fts db unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	var v string
	err := fs.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	if err != nil {
		return "", err
	}
	return v, nil
}

func (fs *memoryFTSStore) writeMetaLocked(ctx context.Context, key, value string) error {
	if fs.db == nil {
		return fmt.Errorf("fts db unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	_, err := fs.db.ExecContext(ctx,
		`INSERT INTO meta(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key,
		value,
	)
	return err
}

func (fs *memoryFTSStore) rebuildLocked(ctx context.Context, sources []memoryVectorSourceFile, fingerprint string) error {
	if fs.db == nil {
		return fmt.Errorf("fts db unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	docs, err := fs.buildDocumentsLocked(sources)
	if err != nil {
		return err
	}

	tx, err := fs.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	rollback := true
	defer func() {
		if rollback {
			_ = tx.Rollback()
		}
	}()

	if _, err := tx.ExecContext(ctx, `DELETE FROM docs`); err != nil {
		return err
	}

	stmt, err := tx.PrepareContext(ctx, `INSERT INTO docs(source, text) VALUES(?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, doc := range docs {
		if strings.TrimSpace(doc.Source) == "" || strings.TrimSpace(doc.Text) == "" {
			continue
		}
		if _, err := stmt.ExecContext(ctx, doc.Source, doc.Text); err != nil {
			return err
		}
	}

	// meta writes are outside of the transaction because meta is not necessarily
	// updated under tx when using separate connections (depending on driver). Use the same tx.
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO meta(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		"fingerprint",
		fingerprint,
	); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO meta(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		"built_at",
		time.Now().Format(time.RFC3339),
	); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	rollback = false
	return nil
}

type memoryFTSDoc struct {
	Source string
	Text   string
}

func (fs *memoryFTSStore) buildDocumentsLocked(sources []memoryVectorSourceFile) ([]memoryFTSDoc, error) {
	docs := make([]memoryFTSDoc, 0, len(sources)*8)

	for _, src := range sources {
		data, err := os.ReadFile(src.Path)
		if err != nil {
			return nil, err
		}
		content := string(data)
		if strings.TrimSpace(content) == "" {
			continue
		}

		if src.RelPath == "MEMORY.md" {
			blocks := parseMemoryAsBlocks(content)
			for _, label := range memoryBlockLabels() {
				for _, entry := range extractBlockEntries(blocks[label]) {
					text := compactWhitespace(entry)
					if text == "" {
						continue
					}
					payload := label + ": " + text
					docs = append(docs, memoryFTSDoc{
						Source: fmt.Sprintf("%s#%s", src.RelPath, label),
						Text:   payload,
					})
				}
			}
			continue
		}

		chunks := chunkMarkdownForVectors(content, memoryVectorChunkChars)
		for idx, chunk := range chunks {
			if strings.TrimSpace(chunk) == "" {
				continue
			}
			docs = append(docs, memoryFTSDoc{
				Source: fmt.Sprintf("%s#%d", src.RelPath, idx+1),
				Text:   chunk,
			})
		}
	}

	return docs, nil
}

func (fs *memoryFTSStore) collectSourceFilesLocked() ([]memoryVectorSourceFile, string, error) {
	sources := make([]memoryVectorSourceFile, 0, 64)

	skipDir := func(name string) bool {
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "", ".", "..":
			return true
		case "fts", "vector", "scopes":
			return true
		}
		if strings.HasPrefix(name, ".") {
			return true
		}
		return false
	}

	walkErr := filepath.WalkDir(fs.memoryDir, func(path string, entry iofs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != fs.memoryDir && skipDir(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}

		if !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			return nil
		}

		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		if info.IsDir() {
			return nil
		}

		rel, relErr := filepath.Rel(fs.memoryDir, path)
		if relErr != nil {
			rel = filepath.Base(path)
		}

		sources = append(sources, memoryVectorSourceFile{
			Path:    path,
			RelPath: filepath.ToSlash(rel),
			Size:    info.Size(),
			ModUnix: info.ModTime().Unix(),
		})
		return nil
	})
	if walkErr != nil {
		return nil, "", walkErr
	}

	sort.Slice(sources, func(i, j int) bool {
		return sources[i].RelPath < sources[j].RelPath
	})

	fingerprint := buildSourceFingerprint(sources, "fts:v1")
	return sources, fingerprint, nil
}

func buildFTSMatchQuery(query string) string {
	parts := strings.Fields(strings.TrimSpace(query))
	if len(parts) == 0 {
		return ""
	}

	// Build a conservative AND query: "foo" AND "bar".
	// This avoids surprising operator behavior and keeps results stable.
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		p = strings.Trim(p, `"'`)
		if p == "" {
			continue
		}
		// Remove characters that break the MATCH grammar.
		// Keep basic punctuation used in identifiers.
		p = strings.Map(func(r rune) rune {
			switch r {
			case '"', '\'', '`':
				return -1
			}
			if r == '\n' || r == '\r' || r == '\t' {
				return ' '
			}
			return r
		}, p)
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, `"`+p+`"`)
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, " AND ")
}
