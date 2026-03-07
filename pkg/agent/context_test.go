package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xwysyy/X-Claw/pkg/providers"
	"github.com/xwysyy/X-Claw/pkg/tools"
)

func testScopedSession(scopeID string) (string, string, string) {
	chatID := fmt.Sprintf("chat-%s", scopeID)
	return "agent:main:feishu:group:" + chatID, "feishu", chatID
}

func testScopedMemoryDir(workspace, sessionKey, channel, chatID string) string {
	scope := deriveMemoryScope(sessionKey, channel, chatID)
	return filepath.Join(workspace, "memory", "scopes", string(scope.Kind), memoryScopeToken(scope.RawID))
}

func setupWorkspace(t *testing.T, files map[string]string) string {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "x-claw-test-*")
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(tmpDir, "memory"), 0o755)
	os.MkdirAll(filepath.Join(tmpDir, "skills"), 0o755)
	for name, content := range files {
		dir := filepath.Dir(filepath.Join(tmpDir, name))
		os.MkdirAll(dir, 0o755)
		if err := os.WriteFile(filepath.Join(tmpDir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return tmpDir
}

func TestSanitizeHistoryForProvider_KeepMultipleToolOutputsFromOneAssistantTurn(t *testing.T) {
	history := []providers.Message{
		{Role: "user", Content: "check two files"},
		{
			Role: "assistant",
			ToolCalls: []providers.ToolCall{
				{ID: "call_1", Name: "read_file"},
				{ID: "call_2", Name: "read_file"},
			},
		},
		{Role: "tool", ToolCallID: "call_1", Content: "file a"},
		{Role: "tool", ToolCallID: "call_2", Content: "file b"},
	}

	got := sanitizeHistoryForProvider(history)

	if len(got) != 4 {
		t.Fatalf("len(got) = %d, want 4; got=%#v", len(got), got)
	}
	if got[2].Role != "tool" || got[2].ToolCallID != "call_1" {
		t.Fatalf("got[2] = %#v, want tool output for call_1", got[2])
	}
	if got[3].Role != "tool" || got[3].ToolCallID != "call_2" {
		t.Fatalf("got[3] = %#v, want tool output for call_2", got[3])
	}
}

func TestSanitizeHistoryForProvider_DropToolOutputWithUnknownCallID(t *testing.T) {
	history := []providers.Message{
		{Role: "user", Content: "check file"},
		{
			Role: "assistant",
			ToolCalls: []providers.ToolCall{
				{ID: "call_1", Name: "read_file"},
			},
		},
		{Role: "tool", ToolCallID: "call_999", Content: "orphan"},
	}

	got := sanitizeHistoryForProvider(history)

	if len(got) != 3 {
		t.Fatalf("len(got) = %d, want 3; got=%#v", len(got), got)
	}
	if got[2].Role != "tool" || got[2].ToolCallID != "call_1" {
		t.Fatalf("expected synthesized placeholder for call_1, got=%#v", got[2])
	}
}

func TestPruneHistoryForContext_ToolResultCondensed(t *testing.T) {
	cb := NewContextBuilder(t.TempDir())
	cb.SetRuntimeSettings(ContextRuntimeSettings{
		ContextWindowTokens:      100,
		PruningMode:              "tools_only",
		SoftToolResultChars:      80,
		HardToolResultChars:      30,
		TriggerRatio:             0.1,
		BootstrapSnapshotEnabled: false,
	})

	history := []providers.Message{
		{Role: "user", Content: "run command"},
		{Role: "tool", Content: strings.Repeat("x", 600)},
		{Role: "assistant", Content: "done"},
		{Role: "user", Content: "next"},
		{Role: "assistant", Content: "ok"},
		{Role: "user", Content: "continue"},
		{Role: "assistant", Content: "ready"},
		{Role: "user", Content: "go"},
		{Role: "assistant", Content: "working"},
		{Role: "user", Content: "status"},
	}

	pruned := cb.pruneHistoryForContext(history, strings.Repeat("S", 500))
	if len(pruned) != len(history) {
		t.Fatalf("len(pruned) = %d, want %d", len(pruned), len(history))
	}
	if !strings.Contains(pruned[1].Content, "tool result") {
		t.Fatalf("expected tool result to be condensed/omitted, got: %q", pruned[1].Content)
	}
}

func TestCompactOldChitChat_CondensesRun(t *testing.T) {
	history := []providers.Message{
		{Role: "user", Content: "ok"},
		{Role: "assistant", Content: "thanks"},
		{Role: "user", Content: "received"},
		{Role: "assistant", Content: "actual content"},
		{Role: "user", Content: "keep recent"},
	}

	got := compactOldChitChat(history, 4)
	if len(got) >= len(history) {
		t.Fatalf("expected condensed history, got len=%d", len(got))
	}
	if !strings.Contains(strings.ToLower(got[0].Content), "condensed") {
		t.Fatalf("expected condensed marker, got: %q", got[0].Content)
	}
}

func TestBuildMessagesForSession_IncludesRetrievedMemory(t *testing.T) {
	workspace := t.TempDir()
	memoryDir := filepath.Join(workspace, "memory")
	if err := os.MkdirAll(memoryDir, 0o755); err != nil {
		t.Fatalf("mkdir memory dir: %v", err)
	}

	memoryContent := `# MEMORY

## Long-term Facts
- Preferred editor is Neovim
`
	if err := os.WriteFile(filepath.Join(memoryDir, "MEMORY.md"), []byte(memoryContent), 0o644); err != nil {
		t.Fatalf("write memory file: %v", err)
	}

	cb := NewContextBuilder(workspace)
	cb.SetRuntimeSettings(ContextRuntimeSettings{
		ContextWindowTokens:      4096,
		PruningMode:              "off",
		BootstrapSnapshotEnabled: false,
		MemoryVectorEnabled:      true,
		MemoryVectorDimensions:   128,
		MemoryVectorTopK:         3,
		MemoryVectorMinScore:     0.01,
		MemoryVectorMaxChars:     800,
		MemoryVectorRecentDays:   7,
	})

	messages := cb.BuildMessagesForSession(
		"sess-1",
		nil,
		"",
		"Which editor do I usually prefer?",
		nil,
		"",
		"",
		nil,
	)
	if len(messages) == 0 {
		t.Fatalf("expected at least one message")
	}

	system := strings.ToLower(messages[0].Content)
	if !strings.Contains(system, "retrieved memory") {
		t.Fatalf("expected retrieved memory section in system prompt, got:\n%s", messages[0].Content)
	}
	if !strings.Contains(system, "neovim") {
		t.Fatalf("expected retrieved semantic hit to mention neovim, got:\n%s", messages[0].Content)
	}
}

func TestBuildMessagesForSession_GroupSession_IncludesAgentMemoryBaseline(t *testing.T) {
	workspace := t.TempDir()
	memoryDir := filepath.Join(workspace, "memory")
	if err := os.MkdirAll(memoryDir, 0o755); err != nil {
		t.Fatalf("mkdir memory dir: %v", err)
	}

	memoryContent := `# MEMORY

## Long-term Facts
- arXiv monitoring focus: algorithmic problem synthesis (not generic RAG)
`
	if err := os.WriteFile(filepath.Join(memoryDir, "MEMORY.md"), []byte(memoryContent), 0o644); err != nil {
		t.Fatalf("write memory file: %v", err)
	}

	cb := NewContextBuilder(workspace)
	cb.SetRuntimeSettings(ContextRuntimeSettings{
		ContextWindowTokens:      4096,
		PruningMode:              "off",
		BootstrapSnapshotEnabled: false,
		MemoryVectorEnabled:      false,
	})

	messages := cb.BuildMessagesForSession(
		"agent:main:feishu:group:oc_test",
		nil,
		"",
		"我的早报呢",
		nil,
		"feishu",
		"oc_test",
		nil,
	)
	if len(messages) == 0 {
		t.Fatalf("expected at least one message")
	}

	system := strings.ToLower(messages[0].Content)
	if !strings.Contains(system, "algorithmic problem synthesis") {
		t.Fatalf("expected agent memory baseline to be present in group session prompt, got:\n%s", messages[0].Content)
	}
}

func TestMemoryForSession_BoundsScopedCache(t *testing.T) {
	workspace := t.TempDir()
	cb := NewContextBuilder(workspace)
	cb.memoryScopesMaxEntries = 3

	for i := range 12 {
		sessionKey, channel, chatID := testScopedSession(fmt.Sprintf("%02d", i))
		store := cb.MemoryForSession(sessionKey, channel, chatID)
		if store == nil {
			t.Fatalf("nil scoped store for %s", sessionKey)
		}
		if got := len(cb.memoryScopes); got > cb.memoryScopesMaxEntries {
			t.Fatalf("len(memoryScopes) = %d, want <= %d", got, cb.memoryScopesMaxEntries)
		}
	}
}

func TestMemoryForSession_RebuildsEvictedScopeWithoutBehaviorChange(t *testing.T) {
	workspace := t.TempDir()
	cb := NewContextBuilder(workspace)
	cb.memoryScopesMaxEntries = 3

	firstSessionKey, firstChannel, firstChatID := testScopedSession("first")
	firstStore := cb.MemoryForSession(firstSessionKey, firstChannel, firstChatID)
	if firstStore == nil {
		t.Fatal("expected first scoped store")
	}
	if err := firstStore.WriteLongTerm("# MEMORY\n\n## Long-term Facts\n- persisted scoped fact\n"); err != nil {
		t.Fatalf("write scoped memory: %v", err)
	}
	firstDir := testScopedMemoryDir(workspace, firstSessionKey, firstChannel, firstChatID)

	for _, id := range []string{"second", "third", "fourth"} {
		sessionKey, channel, chatID := testScopedSession(id)
		if store := cb.MemoryForSession(sessionKey, channel, chatID); store == nil {
			t.Fatalf("expected scoped store for %s", id)
		}
	}

	if got := len(cb.memoryScopes); got > cb.memoryScopesMaxEntries {
		t.Fatalf("len(memoryScopes) = %d, want <= %d", got, cb.memoryScopesMaxEntries)
	}
	if _, ok := cb.memoryScopes[firstDir]; ok {
		t.Fatalf("expected oldest scoped store %q to be evicted", firstDir)
	}

	rebuilt := cb.MemoryForSession(firstSessionKey, firstChannel, firstChatID)
	if rebuilt == nil {
		t.Fatal("expected rebuilt scoped store")
	}
	if rebuilt == firstStore {
		t.Fatal("expected evicted scoped store to be recreated")
	}
	if got := rebuilt.ReadLongTerm(); !strings.Contains(got, "persisted scoped fact") {
		t.Fatalf("rebuilt scoped memory lost persisted content: %q", got)
	}
	if got := cb.MemoryReadForSession(firstSessionKey, firstChannel, firstChatID).GetMemoryContext(); !strings.Contains(got, "persisted scoped fact") {
		t.Fatalf("memory read behavior changed after rebuild: %q", got)
	}
	if got := len(cb.memoryScopes); got > cb.memoryScopesMaxEntries {
		t.Fatalf("len(memoryScopes) after rebuild = %d, want <= %d", got, cb.memoryScopesMaxEntries)
	}
}

func TestSanitizeHistoryForProvider_SynthesizesMissingToolOutputs(t *testing.T) {
	history := []providers.Message{
		{Role: "user", Content: "check file"},
		{
			Role: "assistant",
			ToolCalls: []providers.ToolCall{
				{ID: "call_1", Name: "read_file"},
				{ID: "call_2", Name: "list_dir"},
			},
		},
		{Role: "tool", ToolCallID: "call_1", Content: "ok"},
		{Role: "assistant", Content: "continuing"},
	}

	got := sanitizeHistoryForProvider(history)
	if len(got) != 5 {
		t.Fatalf("len(got) = %d, want 5; got=%#v", len(got), got)
	}
	if got[3].Role != "tool" || got[3].ToolCallID != "call_2" {
		t.Fatalf("expected synthesized tool output for call_2 at index 3, got %#v", got[3])
	}
	if !strings.Contains(strings.ToLower(got[3].Content), "synthesized") {
		t.Fatalf("expected synthesized marker, got %q", got[3].Content)
	}
}

func TestPruneHistoryForContext_DoesNotPanicAfterChitChatCompaction(t *testing.T) {
	cb := NewContextBuilder(t.TempDir())
	cb.SetRuntimeSettings(ContextRuntimeSettings{
		ContextWindowTokens:      100,
		PruningMode:              "tools_only",
		IncludeOldChitChat:       true,
		SoftToolResultChars:      80,
		HardToolResultChars:      30,
		TriggerRatio:             0.2,
		BootstrapSnapshotEnabled: false,
	})

	history := []providers.Message{
		{Role: "user", Content: "ok"},
		{Role: "assistant", Content: "thanks"},
		{Role: "user", Content: "ok"},
		{Role: "assistant", Content: "thanks"},
		{Role: "user", Content: "ok"},
		{Role: "assistant", Content: "thanks"},
		{Role: "tool", Content: strings.Repeat("x", 600)},
		{Role: "assistant", Content: "ready"},
		{Role: "user", Content: "go"},
	}

	_ = cb.pruneHistoryForContext(history, strings.Repeat("S", 500))
}

func TestSingleSystemMessage(t *testing.T) {
	tmpDir := setupWorkspace(t, map[string]string{
		"IDENTITY.md": "# Identity\nTest agent.",
	})
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	tests := []struct {
		name    string
		history []providers.Message
		summary string
		message string
	}{
		{
			name:    "no summary, no history",
			summary: "",
			message: "hello",
		},
		{
			name:    "with summary",
			summary: "Previous conversation discussed X",
			message: "hello",
		},
		{
			name: "with history and summary",
			history: []providers.Message{
				{Role: "user", Content: "hi"},
				{Role: "assistant", Content: "hello"},
			},
			summary: strings.Repeat("Long summary text. ", 50),
			message: "new message",
		},
		{
			name: "system message in history is filtered",
			history: []providers.Message{
				{Role: "system", Content: "stale system prompt from previous session"},
				{Role: "user", Content: "hi"},
				{Role: "assistant", Content: "hello"},
			},
			summary: "",
			message: "new message",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msgs := cb.BuildMessages(tt.history, tt.summary, tt.message, nil, "test", "chat1")

			systemCount := 0
			for _, m := range msgs {
				if m.Role == "system" {
					systemCount++
				}
			}
			if systemCount != 1 {
				t.Errorf("expected exactly 1 system message, got %d", systemCount)
			}
			if msgs[0].Role != "system" {
				t.Errorf("first message should be system, got %s", msgs[0].Role)
			}
			if msgs[len(msgs)-1].Role != "user" {
				t.Errorf("last message should be user, got %s", msgs[len(msgs)-1].Role)
			}

			// System message must contain identity (static) and time (dynamic)
			sys := msgs[0].Content
			if !strings.Contains(sys, "X-Claw") {
				t.Error("system message missing identity")
			}
			if !strings.Contains(sys, "Current Time") {
				t.Error("system message missing dynamic time context")
			}

			// Summary handling
			if tt.summary != "" {
				if !strings.Contains(sys, "CONTEXT_SUMMARY:") {
					t.Error("summary present but CONTEXT_SUMMARY prefix missing")
				}
				if !strings.Contains(sys, tt.summary[:20]) {
					t.Error("summary content not found in system message")
				}
			} else {
				if strings.Contains(sys, "CONTEXT_SUMMARY:") {
					t.Error("CONTEXT_SUMMARY should not appear without summary")
				}
			}
		})
	}
}

// TestMtimeAutoInvalidation verifies that the cache detects source file changes
// via mtime without requiring explicit InvalidateCache().
// Fix: original implementation had no auto-invalidation — edits to bootstrap files
// or skills were invisible until process restart.
func TestMtimeAutoInvalidation(t *testing.T) {
	tests := []struct {
		name       string
		file       string // relative path inside workspace
		contentV1  string
		contentV2  string
		checkField string // substring to verify in rebuilt prompt
	}{
		{
			name:       "bootstrap file change",
			file:       "IDENTITY.md",
			contentV1:  "# Original Identity",
			contentV2:  "# Updated Identity",
			checkField: "Updated Identity",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := setupWorkspace(t, map[string]string{tt.file: tt.contentV1})
			defer os.RemoveAll(tmpDir)

			cb := NewContextBuilder(tmpDir)

			sp1 := cb.BuildSystemPromptWithCache()

			// Overwrite file and set future mtime to ensure detection.
			// Use 2s offset for filesystem mtime resolution safety (some FS
			// have 1s or coarser granularity, especially in CI containers).
			fullPath := filepath.Join(tmpDir, tt.file)
			os.WriteFile(fullPath, []byte(tt.contentV2), 0o644)
			future := time.Now().Add(2 * time.Second)
			os.Chtimes(fullPath, future, future)

			// Verify sourceFilesChangedLocked detects the mtime change
			cb.systemPromptMutex.RLock()
			changed := cb.sourceFilesChangedLocked()
			cb.systemPromptMutex.RUnlock()
			if !changed {
				t.Fatalf("sourceFilesChangedLocked() should detect %s change", tt.file)
			}

			// Should auto-rebuild without explicit InvalidateCache()
			sp2 := cb.BuildSystemPromptWithCache()
			if sp1 == sp2 {
				t.Errorf("cache not rebuilt after %s change", tt.file)
			}
			if !strings.Contains(sp2, tt.checkField) {
				t.Errorf("rebuilt prompt missing expected content %q", tt.checkField)
			}
		})
	}

	// Skills directory mtime change
	t.Run("skills dir change", func(t *testing.T) {
		tmpDir := setupWorkspace(t, nil)
		defer os.RemoveAll(tmpDir)

		cb := NewContextBuilder(tmpDir)
		_ = cb.BuildSystemPromptWithCache() // populate cache

		// Touch skills directory (simulate new skill installed)
		skillsDir := filepath.Join(tmpDir, "skills")
		future := time.Now().Add(2 * time.Second)
		os.Chtimes(skillsDir, future, future)

		// Verify sourceFilesChangedLocked detects it (cache is rebuilt)
		// We confirm by checking internal state: a second call should rebuild.
		cb.systemPromptMutex.RLock()
		changed := cb.sourceFilesChangedLocked()
		cb.systemPromptMutex.RUnlock()
		if !changed {
			t.Error("sourceFilesChangedLocked() should detect skills dir mtime change")
		}
	})
}

// TestExplicitInvalidateCache verifies that InvalidateCache() forces a rebuild
// even when source files haven't changed (useful for tests and reload commands).
func TestExplicitInvalidateCache(t *testing.T) {
	tmpDir := setupWorkspace(t, map[string]string{
		"IDENTITY.md": "# Test Identity",
	})
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	sp1 := cb.BuildSystemPromptWithCache()
	cb.InvalidateCache()
	sp2 := cb.BuildSystemPromptWithCache()

	if sp1 != sp2 {
		t.Error("prompt should be identical after invalidate+rebuild when files unchanged")
	}

	// Verify cachedAt was reset
	cb.InvalidateCache()
	cb.systemPromptMutex.RLock()
	if !cb.cachedAt.IsZero() {
		t.Error("cachedAt should be zero after InvalidateCache()")
	}
	cb.systemPromptMutex.RUnlock()
}

// TestCacheStability verifies that the static prompt is stable across repeated calls
// when no files change (regression test for issue #607).
func TestCacheStability(t *testing.T) {
	tmpDir := setupWorkspace(t, map[string]string{
		"IDENTITY.md": "# Identity\nContent",
		"SOUL.md":     "# Soul\nContent",
	})
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	results := make([]string, 5)
	for i := range results {
		results[i] = cb.BuildSystemPromptWithCache()
	}
	for i := 1; i < len(results); i++ {
		if results[i] != results[0] {
			t.Errorf("cached prompt changed between call 0 and %d", i)
		}
	}

	// Static prompt must NOT contain per-request data
	if strings.Contains(results[0], "Current Time") {
		t.Error("static cached prompt should not contain time (added dynamically)")
	}
}

// TestNewFileCreationInvalidatesCache verifies that creating a tracked source file
// that did not exist when the cache was built triggers a cache rebuild.
// This catches the "from nothing to something" edge case that the old
// modifiedSince (return false on stat error) would miss.
func TestNewFileCreationInvalidatesCache(t *testing.T) {
	tests := []struct {
		name       string
		file       string // relative path inside workspace
		content    string
		checkField string // substring to verify in rebuilt prompt
	}{
		{
			name:       "new bootstrap file",
			file:       "SOUL.md",
			content:    "# Soul\nBe kind and helpful.",
			checkField: "Be kind and helpful",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Start with an empty workspace (no bootstrap/memory files)
			tmpDir := setupWorkspace(t, nil)
			defer os.RemoveAll(tmpDir)

			cb := NewContextBuilder(tmpDir)

			// Populate cache — file does not exist yet
			sp1 := cb.BuildSystemPromptWithCache()
			if strings.Contains(sp1, tt.checkField) {
				t.Fatalf("prompt should not contain %q before file is created", tt.checkField)
			}

			// Create the file after cache was built
			fullPath := filepath.Join(tmpDir, tt.file)
			os.MkdirAll(filepath.Dir(fullPath), 0o755)
			if err := os.WriteFile(fullPath, []byte(tt.content), 0o644); err != nil {
				t.Fatal(err)
			}
			// Set future mtime to guarantee detection
			future := time.Now().Add(2 * time.Second)
			os.Chtimes(fullPath, future, future)

			// Cache should auto-invalidate because file went from absent -> present
			sp2 := cb.BuildSystemPromptWithCache()
			if !strings.Contains(sp2, tt.checkField) {
				t.Errorf("cache not invalidated on new file creation: expected %q in prompt", tt.checkField)
			}
		})
	}
}

// TestSkillFileContentChange verifies that modifying a skill file's content
// (not just the directory structure) invalidates the cache.
// This is the scenario where directory mtime alone is insufficient — on most
// filesystems, editing a file inside a directory does NOT update the parent
// directory's mtime.
func TestSkillFileContentChange(t *testing.T) {
	skillMD := `---
name: test-skill
description: "A test skill"
---
# Test Skill v1
Original content.`

	tmpDir := setupWorkspace(t, map[string]string{
		"skills/test-skill/SKILL.md": skillMD,
	})
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	// Populate cache
	sp1 := cb.BuildSystemPromptWithCache()
	_ = sp1 // cache is warm

	// Modify the skill file content (without touching the skills/ directory)
	updatedSkillMD := `---
name: test-skill
description: "An updated test skill"
---
# Test Skill v2
Updated content.`

	skillPath := filepath.Join(tmpDir, "skills", "test-skill", "SKILL.md")
	if err := os.WriteFile(skillPath, []byte(updatedSkillMD), 0o644); err != nil {
		t.Fatal(err)
	}
	// Set future mtime on the skill file only (NOT the directory)
	future := time.Now().Add(2 * time.Second)
	os.Chtimes(skillPath, future, future)

	// Verify that sourceFilesChangedLocked detects the content change
	cb.systemPromptMutex.RLock()
	changed := cb.sourceFilesChangedLocked()
	cb.systemPromptMutex.RUnlock()
	if !changed {
		t.Error("sourceFilesChangedLocked() should detect skill file content change")
	}

	// Verify cache is actually rebuilt with new content
	sp2 := cb.BuildSystemPromptWithCache()
	if sp1 == sp2 && strings.Contains(sp1, "test-skill") {
		// If the skill appeared in the prompt and the prompt didn't change,
		// the cache was not invalidated.
		t.Error("cache should be invalidated when skill file content changes")
	}
}

// TestGlobalSkillFileContentChange verifies that modifying a global skill
// (~/.x-claw/skills) invalidates the cached system prompt.
func TestGlobalSkillFileContentChange(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	tmpDir := setupWorkspace(t, nil)
	defer os.RemoveAll(tmpDir)

	globalSkillPath := filepath.Join(tmpHome, ".x-claw", "skills", "global-skill", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(globalSkillPath), 0o755); err != nil {
		t.Fatal(err)
	}
	v1 := `---
name: global-skill
description: global-v1
---
# Global Skill v1`
	if err := os.WriteFile(globalSkillPath, []byte(v1), 0o644); err != nil {
		t.Fatal(err)
	}

	cb := NewContextBuilder(tmpDir)
	sp1 := cb.BuildSystemPromptWithCache()
	if !strings.Contains(sp1, "global-v1") {
		t.Fatal("expected initial prompt to contain global skill description")
	}

	v2 := `---
name: global-skill
description: global-v2
---
# Global Skill v2`
	if err := os.WriteFile(globalSkillPath, []byte(v2), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(globalSkillPath, future, future); err != nil {
		t.Fatalf("failed to update mtime for %s: %v", globalSkillPath, err)
	}

	cb.systemPromptMutex.RLock()
	changed := cb.sourceFilesChangedLocked()
	cb.systemPromptMutex.RUnlock()
	if !changed {
		t.Fatal("sourceFilesChangedLocked() should detect global skill file content change")
	}

	sp2 := cb.BuildSystemPromptWithCache()
	if !strings.Contains(sp2, "global-v2") {
		t.Error("rebuilt prompt should contain updated global skill description")
	}
	if sp1 == sp2 {
		t.Error("cache should be invalidated when global skill file content changes")
	}
}

// TestBuiltinSkillFileContentChange verifies that modifying a builtin skill
// invalidates the cached system prompt.
func TestBuiltinSkillFileContentChange(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	tmpDir := setupWorkspace(t, nil)
	defer os.RemoveAll(tmpDir)

	builtinRoot := t.TempDir()
	t.Setenv("X_CLAW_BUILTIN_SKILLS", builtinRoot)

	builtinSkillPath := filepath.Join(builtinRoot, "builtin-skill", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(builtinSkillPath), 0o755); err != nil {
		t.Fatal(err)
	}
	v1 := `---
name: builtin-skill
description: builtin-v1
---
# Builtin Skill v1`
	if err := os.WriteFile(builtinSkillPath, []byte(v1), 0o644); err != nil {
		t.Fatal(err)
	}

	cb := NewContextBuilder(tmpDir)
	sp1 := cb.BuildSystemPromptWithCache()
	if !strings.Contains(sp1, "builtin-v1") {
		t.Fatal("expected initial prompt to contain builtin skill description")
	}

	v2 := `---
name: builtin-skill
description: builtin-v2
---
# Builtin Skill v2`
	if err := os.WriteFile(builtinSkillPath, []byte(v2), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(builtinSkillPath, future, future); err != nil {
		t.Fatalf("failed to update mtime for %s: %v", builtinSkillPath, err)
	}

	cb.systemPromptMutex.RLock()
	changed := cb.sourceFilesChangedLocked()
	cb.systemPromptMutex.RUnlock()
	if !changed {
		t.Fatal("sourceFilesChangedLocked() should detect builtin skill file content change")
	}

	sp2 := cb.BuildSystemPromptWithCache()
	if !strings.Contains(sp2, "builtin-v2") {
		t.Error("rebuilt prompt should contain updated builtin skill description")
	}
	if sp1 == sp2 {
		t.Error("cache should be invalidated when builtin skill file content changes")
	}
}

// TestSkillFileDeletionInvalidatesCache verifies that deleting a nested skill
// file invalidates the cached system prompt.
func TestSkillFileDeletionInvalidatesCache(t *testing.T) {
	tmpDir := setupWorkspace(t, map[string]string{
		"skills/delete-me/SKILL.md": `---
name: delete-me
description: delete-me-v1
---
# Delete Me`,
	})
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)
	sp1 := cb.BuildSystemPromptWithCache()
	if !strings.Contains(sp1, "delete-me-v1") {
		t.Fatal("expected initial prompt to contain skill description")
	}

	skillPath := filepath.Join(tmpDir, "skills", "delete-me", "SKILL.md")
	if err := os.Remove(skillPath); err != nil {
		t.Fatal(err)
	}

	cb.systemPromptMutex.RLock()
	changed := cb.sourceFilesChangedLocked()
	cb.systemPromptMutex.RUnlock()
	if !changed {
		t.Fatal("sourceFilesChangedLocked() should detect deleted skill file")
	}

	sp2 := cb.BuildSystemPromptWithCache()
	if strings.Contains(sp2, "delete-me-v1") {
		t.Error("rebuilt prompt should not contain deleted skill description")
	}
	if sp1 == sp2 {
		t.Error("cache should be invalidated when skill file is deleted")
	}
}

// TestConcurrentBuildSystemPromptWithCache verifies that multiple goroutines
// can safely call BuildSystemPromptWithCache concurrently without producing
// empty results, panics, or data races.
// Run with: go test -race ./pkg/agent/ -run TestConcurrentBuildSystemPromptWithCache
func TestConcurrentBuildSystemPromptWithCache(t *testing.T) {
	tmpDir := setupWorkspace(t, map[string]string{
		"IDENTITY.md":          "# Identity\nConcurrency test agent.",
		"SOUL.md":              "# Soul\nBe helpful.",
		"memory/MEMORY.md":     "# Memory\nUser prefers Go.",
		"skills/demo/SKILL.md": "---\nname: demo\ndescription: \"demo skill\"\n---\n# Demo",
	})
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	const goroutines = 20
	const iterations = 50

	var wg sync.WaitGroup
	errs := make(chan string, goroutines*iterations)

	for g := range goroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := range iterations {
				result := cb.BuildSystemPromptWithCache()
				if result == "" {
					errs <- "empty prompt returned"
					return
				}
				if !strings.Contains(result, "X-Claw") {
					errs <- "prompt missing identity"
					return
				}

				// Also exercise BuildMessages concurrently
				msgs := cb.BuildMessages(nil, "", "hello", nil, "test", "chat")
				if len(msgs) < 2 {
					errs <- "BuildMessages returned fewer than 2 messages"
					return
				}
				if msgs[0].Role != "system" {
					errs <- "first message not system"
					return
				}

				// Occasionally invalidate to exercise the write path
				if i%10 == 0 {
					cb.InvalidateCache()
				}
			}
		}(g)
	}

	wg.Wait()
	close(errs)

	for errMsg := range errs {
		t.Errorf("concurrent access error: %s", errMsg)
	}
}

func TestContextBuilderConcurrentSettersAndReaders(t *testing.T) {
	tmpDir := setupWorkspace(t, map[string]string{
		"IDENTITY.md":      "# Identity\nConcurrency test agent.",
		"SOUL.md":          "# Soul\nBe helpful.",
		"memory/MEMORY.md": "# Memory\nUser prefers Go.",
	})
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)
	history := []providers.Message{{Role: "user", Content: "hello"}}

	const goroutines = 5
	const iterations = 80

	start := make(chan struct{})
	errs := make(chan string, goroutines*iterations)
	var wg sync.WaitGroup

	wg.Add(goroutines)

	go func() {
		defer wg.Done()
		<-start
		for i := range iterations {
			cb.SetRuntimeSettings(ContextRuntimeSettings{
				ContextWindowTokens:      1024 + i,
				PruningMode:              "tools_only",
				IncludeOldChitChat:       i%2 == 0,
				SoftToolResultChars:      256 + i,
				HardToolResultChars:      64 + (i % 16),
				TriggerRatio:             0.6,
				BootstrapSnapshotEnabled: i%2 == 0,
				MemoryVectorEnabled:      i%2 == 0,
				MemoryVectorTopK:         3 + (i % 3),
				MemoryVectorMinScore:     0.05,
				MemoryVectorMaxChars:     600 + i,
				MemoryVectorRecentDays:   7,
			})
		}
	}()

	go func() {
		defer wg.Done()
		<-start
		for i := range iterations {
			cb.SetWebEvidenceMode(i%2 == 0, 2+(i%3))
		}
	}()

	go func() {
		defer wg.Done()
		<-start
		for range iterations {
			cb.SetToolsRegistry(tools.NewToolRegistry())
		}
	}()

	go func() {
		defer wg.Done()
		<-start
		for i := range iterations {
			msgs := cb.BuildMessagesForSession(
				"sess-1",
				history,
				"",
				"message",
				nil,
				"feishu",
				"chat-1",
				nil,
			)
			if len(msgs) == 0 {
				errs <- "BuildMessagesForSession returned no messages"
				return
			}
			if i%10 == 0 {
				cb.InvalidateCache()
			}
		}
	}()

	go func() {
		defer wg.Done()
		<-start
		for range iterations {
			if prompt := cb.BuildSystemPromptWithCache(); prompt == "" {
				errs <- "BuildSystemPromptWithCache returned empty prompt"
				return
			}
		}
	}()

	close(start)
	wg.Wait()
	close(errs)

	for errMsg := range errs {
		t.Error(errMsg)
	}
}

// BenchmarkBuildMessagesWithCache measures caching performance.

// TestEmptyWorkspaceBaselineDetectsNewFiles verifies that when the cache is
// built on an empty workspace (no tracked files exist), creating a file
// afterwards still triggers cache invalidation. This validates the
// time.Unix(1, 0) fallback for maxMtime: any real file's mtime is after epoch,
// so fileChangedSince correctly detects the absent -> present transition AND
// the mtime comparison succeeds even without artificially inflated Chtimes.
func TestEmptyWorkspaceBaselineDetectsNewFiles(t *testing.T) {
	// Empty workspace: no bootstrap files, no memory, no skills content.
	tmpDir := setupWorkspace(t, nil)
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	// Build cache — all tracked files are absent, maxMtime falls back to epoch.
	sp1 := cb.BuildSystemPromptWithCache()

	// Create a bootstrap file with natural mtime (no Chtimes manipulation).
	// The file's mtime should be the current wall-clock time, which is
	// strictly after time.Unix(1, 0).
	soulPath := filepath.Join(tmpDir, "SOUL.md")
	if err := os.WriteFile(soulPath, []byte("# Soul\nNewly created."), 0o644); err != nil {
		t.Fatal(err)
	}

	// Cache should detect the new file via existedAtCache (absent -> present).
	cb.systemPromptMutex.RLock()
	changed := cb.sourceFilesChangedLocked()
	cb.systemPromptMutex.RUnlock()
	if !changed {
		t.Fatal("sourceFilesChangedLocked should detect newly created file on empty workspace")
	}

	sp2 := cb.BuildSystemPromptWithCache()
	if !strings.Contains(sp2, "Newly created") {
		t.Error("rebuilt prompt should contain new file content")
	}
	if sp1 == sp2 {
		t.Error("cache should have been invalidated after file creation")
	}
}

// BenchmarkBuildMessagesWithCache measures caching performance.
func BenchmarkBuildMessagesWithCache(b *testing.B) {
	tmpDir, _ := os.MkdirTemp("", "x-claw-bench-*")
	defer os.RemoveAll(tmpDir)

	os.MkdirAll(filepath.Join(tmpDir, "memory"), 0o755)
	os.MkdirAll(filepath.Join(tmpDir, "skills"), 0o755)
	for _, name := range []string{"IDENTITY.md", "SOUL.md", "USER.md"} {
		os.WriteFile(filepath.Join(tmpDir, name), []byte(strings.Repeat("Content.\n", 10)), 0o644)
	}

	cb := NewContextBuilder(tmpDir)
	history := []providers.Message{
		{Role: "user", Content: "previous message"},
		{Role: "assistant", Content: "previous response"},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cb.BuildMessages(history, "summary", "new message", nil, "cli", "test")
	}
}
