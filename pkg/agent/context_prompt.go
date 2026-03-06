package agent

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/xwysyy/X-Claw/pkg/logger"
)

func (cb *ContextBuilder) buildToolsSection() string {
	if cb.tools == nil {
		return ""
	}

	summaries := cb.tools.GetSummaries()
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
