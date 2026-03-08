package agent

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

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
