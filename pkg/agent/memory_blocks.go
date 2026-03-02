package agent

import (
	"strings"
	"time"
	"unicode/utf8"
)

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
