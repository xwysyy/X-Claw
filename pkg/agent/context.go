package agent

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/xwysyy/X-Claw/pkg/logger"
	"github.com/xwysyy/X-Claw/pkg/providers"
	"github.com/xwysyy/X-Claw/pkg/skills"
	"github.com/xwysyy/X-Claw/pkg/tools"
	"github.com/xwysyy/X-Claw/pkg/utils"
)

type ContextRuntimeSettings struct {
	ContextWindowTokens      int
	PruningMode              string
	IncludeOldChitChat       bool
	SoftToolResultChars      int
	HardToolResultChars      int
	TriggerRatio             float64
	BootstrapSnapshotEnabled bool
	MemoryVectorEnabled      bool
	MemoryVectorDimensions   int
	MemoryVectorTopK         int
	MemoryVectorMinScore     float64
	MemoryVectorMaxChars     int
	MemoryVectorRecentDays   int
	MemoryVectorEmbedding    MemoryVectorEmbeddingSettings
	MemoryHybrid             MemoryHybridSettings
}

type ContextBuilder struct {
	workspace              string
	skillsLoader           *skills.SkillsLoader
	memory                 *MemoryStore
	runtimeMu              sync.RWMutex
	memoryScopesMu         sync.Mutex
	memoryScopes           map[string]*MemoryStore
	memoryScopesLastUsed   map[string]uint64
	memoryScopesUseTick    uint64
	memoryScopesMaxEntries int
	tools                  *tools.ToolRegistry // Direct reference to tool registry
	settings               ContextRuntimeSettings
	webEvidenceEnabled     bool
	webEvidenceMinDomains  int
	bootstrapMu            sync.RWMutex
	bootstrapCache         map[string]string

	// Cache for static system prompt to avoid rebuilding on every call.
	// Dynamic per-request data (time/session/summary) is appended in BuildMessages.
	systemPromptMutex  sync.RWMutex
	cachedSystemPrompt string
	cachedAt           time.Time // max observed mtime across tracked paths at cache build time

	// existedAtCache tracks which source file paths existed the last time the
	// cache was built. This lets sourceFilesChanged detect files that are newly
	// created (didn't exist at cache time, now exist) or deleted (existed at
	// cache time, now gone) — both of which should trigger a cache rebuild.
	existedAtCache map[string]bool

	// skillFilesAtCache snapshots the skill tree file set and mtimes at cache
	// build time. This catches nested file creations/deletions/mtime changes
	// that may not update the top-level skill root directory mtime.
	skillFilesAtCache map[string]time.Time
}

type contextRuntimeSnapshot struct {
	settings              ContextRuntimeSettings
	tools                 *tools.ToolRegistry
	webEvidenceEnabled    bool
	webEvidenceMinDomains int
}

const defaultContextMemoryScopesMaxEntries = 128

func getGlobalConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".x-claw")
}

func NewContextBuilder(workspace string) *ContextBuilder {
	// builtin skills: skills directory in current project
	// Use the skills/ directory under the current working directory
	builtinSkillsDir := strings.TrimSpace(os.Getenv("X_CLAW_BUILTIN_SKILLS"))
	if builtinSkillsDir == "" {
		wd, _ := os.Getwd()
		builtinSkillsDir = filepath.Join(wd, "skills")
	}
	globalSkillsDir := filepath.Join(getGlobalConfigDir(), "skills")

	defaultSettings := ContextRuntimeSettings{
		PruningMode:              "tools_only",
		IncludeOldChitChat:       true,
		SoftToolResultChars:      2000,
		HardToolResultChars:      350,
		TriggerRatio:             0.8,
		BootstrapSnapshotEnabled: true,
		MemoryVectorEnabled:      true,
		MemoryVectorDimensions:   defaultMemoryVectorDimensions,
		MemoryVectorTopK:         defaultMemoryVectorTopK,
		MemoryVectorMinScore:     defaultMemoryVectorMinScore,
		MemoryVectorMaxChars:     defaultMemoryVectorMaxContextChars,
		MemoryVectorRecentDays:   defaultMemoryVectorRecentDailyDays,
		MemoryHybrid: MemoryHybridSettings{
			FTSWeight:    0.6,
			VectorWeight: 0.4,
		},
	}

	memoryStore := NewMemoryStore(workspace)
	memoryStore.SetVectorSettings(MemoryVectorSettings{
		Enabled:         defaultSettings.MemoryVectorEnabled,
		Dimensions:      defaultSettings.MemoryVectorDimensions,
		TopK:            defaultSettings.MemoryVectorTopK,
		MinScore:        defaultSettings.MemoryVectorMinScore,
		MaxContextChars: defaultSettings.MemoryVectorMaxChars,
		RecentDailyDays: defaultSettings.MemoryVectorRecentDays,
		Embedding:       defaultSettings.MemoryVectorEmbedding,
		Hybrid:          defaultSettings.MemoryHybrid,
	})

	return &ContextBuilder{
		workspace:              workspace,
		skillsLoader:           skills.NewSkillsLoader(workspace, globalSkillsDir, builtinSkillsDir),
		memory:                 memoryStore,
		memoryScopes:           map[string]*MemoryStore{},
		memoryScopesLastUsed:   map[string]uint64{},
		memoryScopesMaxEntries: defaultContextMemoryScopesMaxEntries,
		bootstrapCache:         map[string]string{},
		settings:               defaultSettings,
	}
}

// SetToolsRegistry sets the tools registry for dynamic tool summary generation.
func (cb *ContextBuilder) SetToolsRegistry(registry *tools.ToolRegistry) {
	cb.runtimeMu.Lock()
	cb.tools = registry
	cb.runtimeMu.Unlock()
	cb.InvalidateCache()
}

func (cb *ContextBuilder) SetRuntimeSettings(settings ContextRuntimeSettings) {
	if settings.PruningMode == "" {
		settings.PruningMode = "tools_only"
	}
	if settings.SoftToolResultChars <= 0 {
		settings.SoftToolResultChars = 2000
	}
	if settings.HardToolResultChars <= 0 {
		settings.HardToolResultChars = 350
	}
	if settings.TriggerRatio <= 0 || settings.TriggerRatio >= 1 {
		settings.TriggerRatio = 0.8
	}
	if settings.MemoryVectorDimensions <= 0 {
		settings.MemoryVectorDimensions = defaultMemoryVectorDimensions
	}
	if settings.MemoryVectorTopK <= 0 {
		settings.MemoryVectorTopK = defaultMemoryVectorTopK
	}
	if settings.MemoryVectorMinScore < 0 || settings.MemoryVectorMinScore >= 1 {
		settings.MemoryVectorMinScore = defaultMemoryVectorMinScore
	}
	if settings.MemoryVectorMaxChars <= 0 {
		settings.MemoryVectorMaxChars = defaultMemoryVectorMaxContextChars
	}
	if settings.MemoryVectorRecentDays <= 0 {
		settings.MemoryVectorRecentDays = defaultMemoryVectorRecentDailyDays
	}
	settings.MemoryHybrid = normalizeMemoryHybridSettings(settings.MemoryHybrid)

	vecSettings := MemoryVectorSettings{
		Enabled:         settings.MemoryVectorEnabled,
		Dimensions:      settings.MemoryVectorDimensions,
		TopK:            settings.MemoryVectorTopK,
		MinScore:        settings.MemoryVectorMinScore,
		MaxContextChars: settings.MemoryVectorMaxChars,
		RecentDailyDays: settings.MemoryVectorRecentDays,
		Embedding:       settings.MemoryVectorEmbedding,
		Hybrid:          settings.MemoryHybrid,
	}
	cb.runtimeMu.Lock()
	cb.settings = settings
	cb.memory.SetVectorSettings(vecSettings)

	cb.memoryScopesMu.Lock()
	for _, scoped := range cb.memoryScopes {
		if scoped != nil {
			scoped.SetVectorSettings(vecSettings)
		}
	}
	cb.memoryScopesMu.Unlock()
	cb.runtimeMu.Unlock()
}

// SetWebEvidenceMode enables/disables web "evidence mode" instructions in the system prompt.
//
// When enabled, the assistant is instructed to cite at least N sources from distinct domains
// for fact/latest-information answers.
func (cb *ContextBuilder) SetWebEvidenceMode(enabled bool, minDomains int) {
	if cb == nil {
		return
	}
	if minDomains <= 0 {
		minDomains = 2
	}
	cb.runtimeMu.Lock()
	cb.webEvidenceEnabled = enabled
	cb.webEvidenceMinDomains = minDomains
	cb.runtimeMu.Unlock()
	cb.InvalidateCache()
}

func (cb *ContextBuilder) getIdentity() string {
	workspacePath, _ := filepath.Abs(filepath.Join(cb.workspace))
	runtimeSnapshot := cb.runtimeSnapshot()

	// Build tools section dynamically
	toolsSection := buildToolsSection(runtimeSnapshot.tools)

	webEvidenceRule := ""
	if runtimeSnapshot.webEvidenceEnabled {
		minDomains := runtimeSnapshot.webEvidenceMinDomains
		if minDomains <= 0 {
			minDomains = 2
		}
		webEvidenceRule = fmt.Sprintf(
			"\n\n8. **Web evidence mode** — When answering facts or latest information from the web, cite at least %d sources from distinct domains (URLs). "+
				"Never fabricate citations. If evidence is insufficient, explicitly state uncertainty and suggest verification steps.",
			minDomains,
		)
	}

	return fmt.Sprintf(`# X-Claw 🦞
	
	You are X-Claw, a helpful AI assistant.

## Workspace
Your workspace is at: %s
- Agent Memory (shared): %s/memory/MEMORY.md
- Scoped Memory (per user/session): %s/memory/scopes/{user|session}/*/MEMORY.md
- Daily Notes: %s/memory/** and %s/memory/scopes/** (YYYYMM/YYYYMMDD.md)
- Skills: %s/skills/{skill-name}/SKILL.md

%s

## Decision Process

When you receive a task, follow these steps:

1. **Understand** — Identify what the user actually needs. If the request is ambiguous, ask for clarification before acting.
2. **Plan** — Determine which tools and steps are needed. For complex tasks (3+ steps), briefly outline your approach.
3. **Execute** — Use tools one step at a time. Check each result before proceeding to the next step.
4. **Verify** — Confirm the result matches the user's intent. If it doesn't, adjust and retry.
5. **Respond** — Provide a concise summary of what was done and the outcome.

## Important Rules

1. **ALWAYS use tools** — When you need to perform an action (schedule reminders, send messages, execute commands, etc.), you MUST call the appropriate tool. Do NOT just say you'll do it or pretend to do it.

2. **Be helpful and accurate** — When using tools, briefly explain what you're doing.

3. **Memory** — Memory is scoped by session (DM -> user scope, group -> session scope) to avoid cross-channel contamination. Prefer the memory_search / memory_get tools; avoid editing memory files directly unless explicitly asked.

4. **Context summaries** — Conversation summaries provided as context are approximate references only. They may be incomplete or outdated. Always defer to explicit user instructions over summary content.

5. **Honesty** — If you cannot complete a task, explain WHY clearly. Do NOT fabricate results or pretend an action succeeded. If tool results are ambiguous, present the raw data and let the user decide.

6. **Error recovery** — If a tool fails, read the error message and try an alternative approach. Do NOT repeat the same failed tool call with identical arguments. If you've tried 3+ approaches without progress, explain what you've tried and ask for help.

7. **Avoid loops** — NEVER call the same tool with the same arguments more than twice. If you find yourself repeating actions, stop and reassess your approach.%s`,
		workspacePath, workspacePath, workspacePath, workspacePath, workspacePath, workspacePath, toolsSection, webEvidenceRule)
}

var errWalkStop = errors.New("walk stop")

// skillFilesChangedSince compares the current recursive skill file tree
// against the cache-time snapshot. Any create/delete/mtime drift invalidates
// the cache.
func skillFilesChangedSince(skillRoots []string, filesAtCache map[string]time.Time) bool {
	// Defensive: if the snapshot was never initialized, force rebuild.
	if filesAtCache == nil {
		return true
	}

	// Check cached files still exist and keep the same mtime.
	for path, cachedMtime := range filesAtCache {
		info, err := os.Stat(path)
		if err != nil {
			// A previously tracked file disappeared (or became inaccessible):
			// either way, cached skill summary may now be stale.
			return true
		}
		if !info.ModTime().Equal(cachedMtime) {
			return true
		}
	}

	// Check no new files appeared under any skill root.
	changed := false
	for _, root := range skillRoots {
		if strings.TrimSpace(root) == "" {
			continue
		}

		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				// Treat unexpected walk errors as changed to avoid stale cache.
				if !os.IsNotExist(walkErr) {
					changed = true
					return errWalkStop
				}
				return nil
			}
			if d.IsDir() {
				return nil
			}
			if _, ok := filesAtCache[path]; !ok {
				changed = true
				return errWalkStop
			}
			return nil
		})

		if changed {
			return true
		}
		if err != nil && !errors.Is(err, errWalkStop) && !os.IsNotExist(err) {
			logger.DebugCF("agent", "skills walk error", map[string]any{"error": err.Error()})
			return true
		}
	}

	return false
}

func (cb *ContextBuilder) LoadBootstrapFiles(sessionKey string) string {
	sessionKey = utils.CanonicalSessionKey(sessionKey)
	settings := cb.settingsSnapshot()

	if settings.BootstrapSnapshotEnabled && sessionKey != "" {
		cb.bootstrapMu.RLock()
		cached, ok := cb.bootstrapCache[sessionKey]
		cb.bootstrapMu.RUnlock()
		if ok {
			return cached
		}
	}

	bootstrapFiles := []string{
		"AGENTS.md",
		"SOUL.md",
		"USER.md",
		"IDENTITY.md",
	}

	var sb strings.Builder
	for _, filename := range bootstrapFiles {
		filePath := filepath.Join(cb.workspace, filename)
		if data, err := os.ReadFile(filePath); err == nil {
			fmt.Fprintf(&sb, "## %s\n\n%s\n\n", filename, data)
		}
	}

	content := sb.String()
	if settings.BootstrapSnapshotEnabled && sessionKey != "" {
		cb.bootstrapMu.Lock()
		cb.bootstrapCache[sessionKey] = content
		cb.bootstrapMu.Unlock()
	}
	return content
}

func (cb *ContextBuilder) buildDynamicContext(channel, chatID string, ws *WorkingState) string {
	now := time.Now().Format("2006-01-02 15:04 (Monday)")
	rt := fmt.Sprintf("%s %s, Go %s", runtime.GOOS, runtime.GOARCH, runtime.Version())

	var sb strings.Builder
	fmt.Fprintf(&sb, "## Current Time\n%s\n\n## Runtime\n%s", now, rt)
	if channel != "" && chatID != "" {
		fmt.Fprintf(&sb, "\n\n## Current Session\nChannel: %s\nChat ID: %s", channel, chatID)
	}

	// Inject working state if available
	if ws != nil {
		if wsCtx := ws.FormatForContext(); wsCtx != "" {
			fmt.Fprintf(&sb, "\n\n%s", wsCtx)
		}
	}

	return sb.String()
}

func appendPromptSection(
	parts *[]string,
	blocks *[]providers.ContentBlock,
	section string,
	cacheControl *providers.CacheControl,
) {
	if strings.TrimSpace(section) == "" {
		return
	}
	*parts = append(*parts, section)
	*blocks = append(*blocks, providers.ContentBlock{Type: "text", Text: section, CacheControl: cacheControl})
}

func buildContextSummarySection(summary string) string {
	if strings.TrimSpace(summary) == "" {
		return ""
	}
	return fmt.Sprintf(
		"CONTEXT_SUMMARY: The following is an approximate summary of prior conversation "+
			"for reference only. It may be incomplete or outdated - always defer to explicit instructions.\n\n%s",
		summary,
	)
}

func buildMemoryContextSection(memoryStore MemoryReader) string {
	if memoryStore == nil {
		return ""
	}
	memoryContext := memoryStore.GetMemoryContext()
	if strings.TrimSpace(memoryContext) == "" {
		return ""
	}
	return "# Memory\n\n" + memoryContext
}

func (cb *ContextBuilder) buildRetrievedMemorySection(
	memoryStore MemoryReader,
	currentMessage string,
	settings ContextRuntimeSettings,
) string {
	if memoryStore == nil || !settings.MemoryVectorEnabled || strings.TrimSpace(currentMessage) == "" {
		return ""
	}

	retrievalCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cb.runtimeMu.RLock()
	hits, err := memoryStore.SearchRelevant(retrievalCtx, currentMessage, settings.MemoryVectorTopK, settings.MemoryVectorMinScore)
	cb.runtimeMu.RUnlock()
	if err != nil {
		logger.WarnCF("agent", "Semantic memory retrieval failed", map[string]any{"error": err.Error()})
		return ""
	}

	return formatRetrievedMemoryContext(hits, settings.MemoryVectorMaxChars)
}

func buildUserInputMessage(currentMessage string, media []string) providers.Message {
	msg := providers.Message{
		Role:    "user",
		Content: currentMessage,
	}
	if len(media) > 0 {
		msg.Media = media
	}
	return msg
}

func (cb *ContextBuilder) BuildMessages(
	history []providers.Message,
	summary string,
	currentMessage string,
	media []string,
	channel, chatID string,
) []providers.Message {
	return cb.BuildMessagesForSession(
		"",
		history,
		summary,
		currentMessage,
		media,
		channel,
		chatID,
		nil,
	)
}

func (cb *ContextBuilder) BuildMessagesForSession(
	sessionKey string,
	history []providers.Message,
	summary string,
	currentMessage string,
	media []string,
	channel, chatID string,
	ws *WorkingState,
) []providers.Message {
	messages := []providers.Message{}
	settings := cb.settingsSnapshot()

	memoryStore := cb.MemoryReadForSession(sessionKey, channel, chatID)

	staticPrompt := cb.BuildSystemPromptWithCache()
	dynamicCtx := cb.buildDynamicContext(channel, chatID, ws)

	stringParts := make([]string, 0, 5)
	contentBlocks := make([]providers.ContentBlock, 0, 5)
	appendPromptSection(&stringParts, &contentBlocks, staticPrompt, &providers.CacheControl{Type: "ephemeral"})
	appendPromptSection(&stringParts, &contentBlocks, dynamicCtx, nil)
	appendPromptSection(&stringParts, &contentBlocks, buildContextSummarySection(summary), nil)
	appendPromptSection(&stringParts, &contentBlocks, buildMemoryContextSection(memoryStore), nil)
	appendPromptSection(&stringParts, &contentBlocks, cb.buildRetrievedMemorySection(memoryStore, currentMessage, settings), nil)

	fullSystemPrompt := strings.Join(stringParts, "\n\n---\n\n")

	cb.systemPromptMutex.RLock()
	isCached := cb.cachedSystemPrompt != ""
	cb.systemPromptMutex.RUnlock()

	logger.DebugCF("agent", "System prompt built",
		map[string]any{
			"static_chars":  len(staticPrompt),
			"dynamic_chars": len(dynamicCtx),
			"total_chars":   len(fullSystemPrompt),
			"has_summary":   summary != "",
			"cached":        isCached,
		})

	preview := fullSystemPrompt
	if len(preview) > 500 {
		preview = preview[:500] + "... (truncated)"
	}
	logger.DebugCF("agent", "System prompt preview", map[string]any{"preview": preview})

	history = sanitizeHistoryForProvider(history)
	history = cb.pruneHistoryForContext(history, fullSystemPrompt)
	history = sanitizeHistoryForProvider(history)

	messages = append(messages, providers.Message{
		Role:        "system",
		Content:     fullSystemPrompt,
		SystemParts: contentBlocks,
	})

	messages = append(messages, history...)

	if strings.TrimSpace(currentMessage) != "" {
		messages = append(messages, buildUserInputMessage(currentMessage, media))
	}

	return messages
}

func formatRetrievedMemoryContext(hits []MemoryVectorHit, maxChars int) string {
	if len(hits) == 0 {
		return ""
	}
	if maxChars <= 0 {
		maxChars = defaultMemoryVectorMaxContextChars
	}

	var sb strings.Builder
	sb.WriteString("# Retrieved Memory\n\n")
	sb.WriteString("Use these semantic hits as hints; prefer current workspace files when they conflict.\n\n")

	remaining := maxChars
	for _, hit := range hits {
		if remaining <= 0 {
			break
		}
		text := compactWhitespace(hit.Text)
		if text == "" {
			continue
		}
		line := fmt.Sprintf("- (score=%.2f, source=%s) %s\n", hit.Score, hit.Source, text)
		if len(line) > remaining {
			if remaining > 6 {
				line = line[:remaining-4] + "...\n"
			} else {
				break
			}
		}
		sb.WriteString(line)
		remaining -= len(line)
	}

	if remaining == maxChars {
		return ""
	}
	return strings.TrimSpace(sb.String())
}

func (cb *ContextBuilder) AddToolResult(
	messages []providers.Message,
	toolCallID, toolName, result string,
) []providers.Message {
	messages = append(messages, providers.Message{
		Role:       "tool",
		Content:    result,
		ToolCallID: toolCallID,
	})
	return messages
}

func (cb *ContextBuilder) AddAssistantMessage(
	messages []providers.Message,
	content string,
	toolCalls []map[string]any,
) []providers.Message {
	msg := providers.Message{
		Role:    "assistant",
		Content: content,
	}
	// Always add assistant message, whether or not it has tool calls
	messages = append(messages, msg)
	return messages
}

// GetSkillsInfo returns information about loaded skills.
func (cb *ContextBuilder) GetSkillsInfo() map[string]any {
	allSkills := cb.skillsLoader.ListSkills()
	skillNames := make([]string, 0, len(allSkills))
	for _, s := range allSkills {
		skillNames = append(skillNames, s.Name)
	}
	return map[string]any{
		"total":     len(allSkills),
		"available": len(allSkills),
		"names":     skillNames,
	}
}

func (cb *ContextBuilder) pruneHistoryForContext(
	history []providers.Message,
	systemPrompt string,
) []providers.Message {
	settings := cb.settingsSnapshot()
	if len(history) == 0 || settings.PruningMode == "off" || settings.ContextWindowTokens <= 0 {
		return history
	}

	totalTokens := estimateTotalTokens(systemPrompt, history)
	ratio := float64(totalTokens) / float64(settings.ContextWindowTokens)
	if ratio < settings.TriggerRatio {
		return history
	}

	cutoff := len(history) - 8
	if cutoff <= 0 {
		return history
	}

	pruned := make([]providers.Message, 0, len(history))
	for i := 0; i < cutoff; i++ {
		msg := history[i]

		if settings.PruningMode == "tools_only" && msg.Role == "tool" && settings.SoftToolResultChars > 0 {
			raw := msg.Content
			if len(raw) > settings.SoftToolResultChars {
				head := settings.SoftToolResultChars * 7 / 10
				tail := settings.SoftToolResultChars * 2 / 10
				if head+tail > len(raw) {
					head = len(raw)
					tail = 0
				}
				msg.Content = raw[:head] +
					"\n...\n[tool result condensed for context stability]\n...\n" +
					raw[len(raw)-tail:]
			}
		}

		pruned = append(pruned, msg)
	}
	pruned = append(pruned, history[cutoff:]...)

	if settings.IncludeOldChitChat {
		pruned = compactOldChitChat(pruned, cutoff)
	}

	totalTokens = estimateTotalTokens(systemPrompt, pruned)
	ratio = float64(totalTokens) / float64(settings.ContextWindowTokens)
	if ratio < settings.TriggerRatio || settings.HardToolResultChars <= 0 {
		return pruned
	}

	scanLimit := minInt(cutoff, len(pruned))
	for i := 0; i < scanLimit; i++ {
		if ratio < settings.TriggerRatio {
			break
		}
		msg := pruned[i]
		if msg.Role != "tool" || len(msg.Content) <= settings.HardToolResultChars {
			continue
		}
		pruned[i].Content = "[tool result omitted for context stability; details preserved in session history]"
		totalTokens = estimateTotalTokens(systemPrompt, pruned)
		ratio = float64(totalTokens) / float64(settings.ContextWindowTokens)
	}

	return pruned
}

func compactOldChitChat(history []providers.Message, cutoff int) []providers.Message {
	if len(history) == 0 || cutoff <= 0 {
		return history
	}

	isLowSignal := func(msg providers.Message) bool {
		if msg.Role != "user" && msg.Role != "assistant" {
			return false
		}
		if len(msg.ToolCalls) > 0 || msg.ToolCallID != "" {
			return false
		}
		text := strings.ToLower(strings.TrimSpace(msg.Content))
		if text == "" || len(text) > 40 {
			return false
		}
		switch text {
		case "ok", "okay", "thanks", "thank you", "got it", "roger", "understood", "好的", "收到", "谢谢":
			return true
		}
		return false
	}

	result := make([]providers.Message, 0, len(history))
	i := 0
	for i < len(history) {
		if i >= cutoff || !isLowSignal(history[i]) {
			result = append(result, history[i])
			i++
			continue
		}

		j := i
		for j < cutoff && isLowSignal(history[j]) {
			j++
		}
		runLen := j - i
		if runLen >= 2 {
			result = append(result, providers.Message{
				Role:    "assistant",
				Content: fmt.Sprintf("[History note: %d brief acknowledgements condensed]", runLen),
			})
		} else {
			result = append(result, history[i])
		}
		i = j
	}

	return result
}

func sanitizeHistoryForProvider(history []providers.Message) []providers.Message {
	if len(history) == 0 {
		return history
	}

	sanitized := make([]providers.Message, 0, len(history))
	var pendingToolCalls map[string]struct{}
	var pendingToolCallOrder []string
	flushPendingToolCalls := func() {
		if len(pendingToolCalls) == 0 {
			pendingToolCalls = nil
			pendingToolCallOrder = nil
			return
		}
		for _, id := range pendingToolCallOrder {
			if _, ok := pendingToolCalls[id]; !ok {
				continue
			}
			sanitized = append(sanitized, providers.Message{
				Role:       "tool",
				ToolCallID: id,
				Content:    "[tool result missing in transcript; synthesized placeholder for provider compatibility]",
			})
		}
		pendingToolCalls = nil
		pendingToolCallOrder = nil
	}

	for _, msg := range history {
		switch msg.Role {
		case "system":
			logger.DebugCF("agent", "Dropping system message from history", map[string]any{})
			continue

		case "tool":
			if pendingToolCalls == nil {
				logger.DebugCF("agent", "Dropping orphaned tool message", map[string]any{})
				continue
			}

			if len(pendingToolCalls) > 0 {
				if msg.ToolCallID == "" {
					logger.DebugCF("agent", "Dropping orphaned tool message with empty call id", map[string]any{})
					continue
				}
				if _, ok := pendingToolCalls[msg.ToolCallID]; !ok {
					logger.DebugCF(
						"agent",
						"Dropping duplicate/orphaned tool message with unknown call id",
						map[string]any{"tool_call_id": msg.ToolCallID},
					)
					continue
				}
				delete(pendingToolCalls, msg.ToolCallID)
			}
			sanitized = append(sanitized, msg)

		case "assistant":
			flushPendingToolCalls()

			if len(msg.ToolCalls) > 0 {
				if len(sanitized) == 0 {
					logger.DebugCF("agent", "Dropping assistant tool-call turn at history start", map[string]any{})
					continue
				}
				prev := sanitized[len(sanitized)-1]
				if prev.Role != "user" && prev.Role != "tool" {
					logger.DebugCF(
						"agent",
						"Dropping assistant tool-call turn with invalid predecessor",
						map[string]any{"prev_role": prev.Role},
					)
					continue
				}

				pendingToolCalls = make(map[string]struct{}, len(msg.ToolCalls))
				pendingToolCallOrder = make([]string, 0, len(msg.ToolCalls))
				for _, tc := range msg.ToolCalls {
					if tc.ID != "" {
						if _, exists := pendingToolCalls[tc.ID]; exists {
							continue
						}
						pendingToolCalls[tc.ID] = struct{}{}
						pendingToolCallOrder = append(pendingToolCallOrder, tc.ID)
					}
				}
			}
			sanitized = append(sanitized, msg)

		default:
			flushPendingToolCalls()
			sanitized = append(sanitized, msg)
		}
	}
	flushPendingToolCalls()

	return sanitized
}

func estimateMessageTokens(msg providers.Message) int {
	chars := utf8.RuneCountInString(msg.Content)
	for _, tc := range msg.ToolCalls {
		chars += utf8.RuneCountInString(tc.Name)
		if tc.Function != nil {
			chars += utf8.RuneCountInString(tc.Function.Name)
			chars += utf8.RuneCountInString(tc.Function.Arguments)
		}
	}
	if chars == 0 {
		return 0
	}
	return chars * 2 / 5
}

func estimateTotalTokens(systemPrompt string, messages []providers.Message) int {
	total := utf8.RuneCountInString(systemPrompt) * 2 / 5
	for _, msg := range messages {
		total += estimateMessageTokens(msg)
	}
	return total
}

func buildToolsSection(registry *tools.ToolRegistry) string {
	if registry == nil {
		return ""
	}

	summaries := registry.GetSummaries()
	if len(summaries) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Available Tools\n\n")
	sb.WriteString(
		"**CRITICAL**: You MUST use tools to perform actions. Do NOT pretend to execute commands or schedule tasks.\n\n",
	)
	sb.WriteString("You have access to the following tools:\n\n")
	for _, s := range summaries {
		sb.WriteString(s)
		sb.WriteString("\n")
	}

	return sb.String()
}

func (cb *ContextBuilder) BuildSystemPrompt() string {
	return cb.BuildSystemPromptForSession("")
}

func (cb *ContextBuilder) BuildSystemPromptForSession(sessionKey string) string {
	parts := []string{}

	parts = append(parts, cb.getIdentity())

	bootstrapContent := cb.LoadBootstrapFiles(sessionKey)
	if bootstrapContent != "" {
		parts = append(parts, bootstrapContent)
	}

	skillsSummary := cb.skillsLoader.BuildSkillsSummary()
	if skillsSummary != "" {
		parts = append(parts, fmt.Sprintf(`# Skills

The following skills extend your capabilities. To use a skill, read its SKILL.md file using the read_file tool.

%s`, skillsSummary))
	}

	return strings.Join(parts, "\n\n---\n\n")
}

func (cb *ContextBuilder) BuildSystemPromptWithCache() string {
	cb.systemPromptMutex.RLock()
	if cb.cachedSystemPrompt != "" && !cb.sourceFilesChangedLocked() {
		cached := cb.cachedSystemPrompt
		cb.systemPromptMutex.RUnlock()
		return cached
	}
	cb.systemPromptMutex.RUnlock()

	cb.systemPromptMutex.Lock()
	defer cb.systemPromptMutex.Unlock()

	if cb.cachedSystemPrompt != "" && !cb.sourceFilesChangedLocked() {
		return cb.cachedSystemPrompt
	}

	baseline := cb.buildCacheBaseline()
	prompt := cb.BuildSystemPrompt()
	cb.cachedSystemPrompt = prompt
	cb.cachedAt = baseline.maxMtime
	cb.existedAtCache = baseline.existed
	cb.skillFilesAtCache = baseline.skillFiles

	return prompt
}

func (cb *ContextBuilder) InvalidateCache() {
	cb.systemPromptMutex.Lock()
	defer cb.systemPromptMutex.Unlock()

	cb.cachedSystemPrompt = ""
	cb.cachedAt = time.Time{}
	cb.existedAtCache = nil
	cb.skillFilesAtCache = nil

	logger.DebugCF("agent", "System prompt cache invalidated", nil)
}

func (cb *ContextBuilder) sourcePaths() []string {
	return []string{
		filepath.Join(cb.workspace, "AGENTS.md"),
		filepath.Join(cb.workspace, "SOUL.md"),
		filepath.Join(cb.workspace, "USER.md"),
		filepath.Join(cb.workspace, "IDENTITY.md"),
	}
}

func (cb *ContextBuilder) skillRoots() []string {
	if cb.skillsLoader == nil {
		return []string{filepath.Join(cb.workspace, "skills")}
	}

	roots := cb.skillsLoader.SkillRoots()
	if len(roots) == 0 {
		return []string{filepath.Join(cb.workspace, "skills")}
	}
	return roots
}

type cacheBaseline struct {
	existed    map[string]bool
	skillFiles map[string]time.Time
	maxMtime   time.Time
}

func (cb *ContextBuilder) buildCacheBaseline() cacheBaseline {
	skillRoots := cb.skillRoots()
	allPaths := append(cb.sourcePaths(), skillRoots...)

	existed := make(map[string]bool, len(allPaths))
	skillFiles := make(map[string]time.Time)
	var maxMtime time.Time
	for _, p := range allPaths {
		info, err := os.Stat(p)
		existed[p] = err == nil
		if err == nil && info.ModTime().After(maxMtime) {
			maxMtime = info.ModTime()
		}
	}

	for _, root := range skillRoots {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr == nil && !d.IsDir() {
				if info, err := os.Stat(path); err == nil {
					skillFiles[path] = info.ModTime()
					if info.ModTime().After(maxMtime) {
						maxMtime = info.ModTime()
					}
				}
			}
			return nil
		})
	}

	if maxMtime.IsZero() {
		maxMtime = time.Unix(1, 0)
	}

	return cacheBaseline{existed: existed, skillFiles: skillFiles, maxMtime: maxMtime}
}

func (cb *ContextBuilder) sourceFilesChangedLocked() bool {
	if cb.cachedAt.IsZero() {
		return true
	}

	if slices.ContainsFunc(cb.sourcePaths(), cb.fileChangedSince) {
		return true
	}

	for _, root := range cb.skillRoots() {
		if cb.fileChangedSince(root) {
			return true
		}
	}
	if skillFilesChangedSince(cb.skillRoots(), cb.skillFilesAtCache) {
		return true
	}

	return false
}

func (cb *ContextBuilder) fileChangedSince(path string) bool {
	if cb.existedAtCache == nil {
		return true
	}

	info, err := os.Stat(path)
	existedBefore := cb.existedAtCache[path]
	existsNow := err == nil
	if existedBefore != existsNow {
		return true
	}
	if err == nil && info.ModTime().After(cb.cachedAt) {
		return true
	}
	return false
}
