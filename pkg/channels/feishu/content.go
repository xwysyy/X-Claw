package feishu

import (
	"encoding/json"
	"html"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

var (
	feishuBrTagRe       = regexp.MustCompile(`(?i)<br\s*/?>`)
	feishuPStartTagRe   = regexp.MustCompile(`(?i)<p[^>]*>`)
	feishuPEndTagRe     = regexp.MustCompile(`(?i)</p>`)
	feishuAnyTagRe      = regexp.MustCompile(`(?s)<[^>]+>`)
	feishuListNewlineRe = regexp.MustCompile(`(?m)(^|\n)([-*]|\d+\.)\s*\n\s*`)
	feishuSpacesRe      = regexp.MustCompile(`[ \t]+`)
	feishuManyNewlineRe = regexp.MustCompile(`\n{3,}`)
)

// normalizeFeishuText converts Feishu's HTML-ish rich-text fragments into a
// predictable plain-text representation suitable for LLM consumption.
//
// It is intentionally conservative: it strips tags, unescapes HTML entities,
// normalizes whitespace/newlines, and fixes list marker quirks like "-\n1" → "- 1".
func normalizeFeishuText(in string) string {
	if strings.TrimSpace(in) == "" {
		return ""
	}

	s := in

	// Normalize line endings early.
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")

	// Convert common tags to newlines.
	s = feishuBrTagRe.ReplaceAllString(s, "\n")
	s = feishuPStartTagRe.ReplaceAllString(s, "")
	s = feishuPEndTagRe.ReplaceAllString(s, "\n")

	// Strip any remaining tags.
	s = feishuAnyTagRe.ReplaceAllString(s, "")

	// Decode entities (&nbsp; etc). html.UnescapeString turns &nbsp; into U+00A0.
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, "\u00a0", " ")

	// Fix list/newline quirks: "-\n1" → "- 1", "2.\nsecond" → "2. second".
	s = feishuListNewlineRe.ReplaceAllString(s, "${1}${2} ")

	// Normalize spaces per-line for deterministic outputs.
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(feishuSpacesRe.ReplaceAllString(lines[i], " "))
	}
	s = strings.Join(lines, "\n")

	// Collapse excessive blank lines (keep at most one empty line).
	s = feishuManyNewlineRe.ReplaceAllString(s, "\n\n")

	return strings.TrimSpace(s)
}

// feishuExtractJSONString extracts the first non-empty string value from raw JSON
// by trying keys in order. It returns "" on invalid JSON or if all values are empty.
func feishuExtractJSONString(raw string, keys ...string) string {
	if strings.TrimSpace(raw) == "" || len(keys) == 0 {
		return ""
	}

	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil || m == nil {
		return ""
	}

	for _, key := range keys {
		v, ok := m[key]
		if !ok {
			continue
		}
		s, ok := v.(string)
		if !ok {
			continue
		}
		s = strings.TrimSpace(s)
		if s != "" {
			return s
		}
	}
	return ""
}

// extractFeishuPostImageKeys finds all image keys inside a post payload JSON.
// It supports both snake_case ("image_key") and camelCase ("imageKey") and
// deduplicates + sorts the result.
//
// Returns nil on invalid JSON.
func extractFeishuPostImageKeys(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}

	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil
	}

	set := make(map[string]struct{})
	var walk func(any)
	walk = func(node any) {
		switch n := node.(type) {
		case map[string]any:
			for k, vv := range n {
				lk := strings.ToLower(strings.TrimSpace(k))
				if lk == "image_key" || lk == "imagekey" {
					if s, ok := vv.(string); ok {
						s = strings.TrimSpace(s)
						if s != "" {
							set[s] = struct{}{}
						}
					}
				}
				walk(vv)
			}
		case []any:
			for _, vv := range n {
				walk(vv)
			}
		}
	}
	walk(v)

	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// extractFeishuPostContent flattens a Feishu "post" message payload into plain text.
//
// It returns "" on invalid JSON.
func extractFeishuPostContent(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}

	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil || m == nil {
		return ""
	}

	title, _ := m["title"].(string)
	title = normalizeFeishuText(title)

	// Prefer zh_cn.content when available; fall back to top-level content for older payloads.
	var content any
	if zh, ok := m["zh_cn"].(map[string]any); ok && zh != nil {
		if c, ok := zh["content"]; ok {
			content = c
		}
	}
	if content == nil {
		content = m["content"]
	}

	var paragraphs []string
	if rows, ok := content.([]any); ok {
		for _, row := range rows {
			switch r := row.(type) {
			case []any:
				p := buildFeishuPostParagraph(r)
				if p != "" {
					paragraphs = append(paragraphs, p)
				}
			case map[string]any:
				p := buildFeishuPostParagraph([]any{r})
				if p != "" {
					paragraphs = append(paragraphs, p)
				}
			}
		}
	}

	var out []string
	if strings.TrimSpace(title) != "" {
		out = append(out, strings.TrimSpace(title))
	}
	out = append(out, paragraphs...)
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func buildFeishuPostParagraph(nodes []any) string {
	if len(nodes) == 0 {
		return ""
	}

	var b strings.Builder
	for _, n := range nodes {
		elem, ok := n.(map[string]any)
		if !ok || elem == nil {
			continue
		}

		tag, _ := elem["tag"].(string)
		tag = strings.ToLower(strings.TrimSpace(tag))

		switch tag {
		case "text", "md":
			text, _ := elem["text"].(string)
			b.WriteString(stripFeishuInlineHTML(text))
		case "a":
			text, _ := elem["text"].(string)
			text = stripFeishuInlineHTML(text)
			if strings.TrimSpace(text) != "" {
				b.WriteString(text)
				break
			}
			href, _ := elem["href"].(string)
			href = strings.TrimSpace(href)
			if href != "" {
				b.WriteString(href)
			}
		case "at":
			// Field names differ between payload variants ("name" vs "user_name").
			name, _ := elem["name"].(string)
			if strings.TrimSpace(name) == "" {
				name, _ = elem["user_name"].(string)
			}
			name = strings.TrimSpace(name)
			if name != "" {
				b.WriteString("@")
				b.WriteString(name)
			}
		case "img":
			// Keep a compact placeholder; the actual image is available via mediaRefs.
			b.WriteString("[image]")
		default:
			// Ignore unknown tags.
		}
	}

	out := b.String()
	out = strings.ReplaceAll(out, "\u00a0", " ")
	out = feishuSpacesRe.ReplaceAllString(out, " ")
	return strings.TrimSpace(out)
}

// stripFeishuInlineHTML strips tags and unescapes entities for inline fragments
// while preserving leading/trailing whitespace (important for post node joins).
func stripFeishuInlineHTML(in string) string {
	if in == "" {
		return ""
	}
	s := in
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = feishuBrTagRe.ReplaceAllString(s, "\n")
	s = feishuPStartTagRe.ReplaceAllString(s, "")
	s = feishuPEndTagRe.ReplaceAllString(s, "\n")
	s = feishuAnyTagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return strings.ReplaceAll(s, "\u00a0", " ")
}

// resolveFeishuFileUploadTypes determines (fileType, messageType) for Feishu uploads.
// It is a small helper intended for stable unit-tested behavior.
func resolveFeishuFileUploadTypes(mediaType, filename, contentType string) (fileType string, messageType string) {
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	contentType = strings.ToLower(strings.TrimSpace(contentType))

	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(strings.TrimSpace(filename)), "."))
	if ext == "" && (strings.HasPrefix(contentType, "video/") || strings.HasPrefix(contentType, "audio/")) {
		parts := strings.SplitN(contentType, "/", 2)
		if len(parts) == 2 {
			sub := strings.TrimSpace(parts[1])
			// Avoid overly generic values like "application/octet-stream".
			if sub != "" && sub != "octet-stream" {
				ext = sub
			}
		}
	}
	if ext == "" {
		ext = "file"
	}

	switch mediaType {
	case "audio":
		// Feishu audio messages are effectively "opus" only; other audio types should fall back to file.
		if ext == "opus" {
			messageType = "audio"
		} else {
			messageType = "file"
		}
	case "video", "media":
		messageType = "media"
	default:
		messageType = "file"
	}

	return ext, messageType
}

// extractFeishuMessageContent converts an inbound Feishu message into plain text.
// It returns raw JSON payloads as-is when decoding fails (so we don't lose evidence).
func extractFeishuMessageContent(msg *larkim.EventMessage) string {
	if msg == nil || msg.Content == nil {
		return ""
	}
	raw := strings.TrimSpace(stringValue(msg.Content))
	if raw == "" {
		return ""
	}

	msgType := ""
	if msg.MessageType != nil {
		msgType = strings.ToLower(strings.TrimSpace(*msg.MessageType))
	}

	switch msgType {
	case "", larkim.MsgTypeText:
		var payload struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			return raw
		}
		return normalizeFeishuText(payload.Text)

	case larkim.MsgTypePost:
		if out := extractFeishuPostContent(raw); strings.TrimSpace(out) != "" {
			return out
		}
		// Preserve raw payload for debugging / evidence.
		return raw

	case larkim.MsgTypeImage:
		return ""

	case larkim.MsgTypeFile:
		if name := extractFileName(raw); strings.TrimSpace(name) != "" {
			return strings.TrimSpace(name)
		}
		return ""

	case larkim.MsgTypeAudio:
		if name := extractFileName(raw); strings.TrimSpace(name) != "" {
			return strings.TrimSpace(name)
		}
		return ""

	case larkim.MsgTypeMedia, "video":
		if name := extractFileName(raw); strings.TrimSpace(name) != "" {
			return strings.TrimSpace(name)
		}
		return ""

	default:
		// Unknown types: keep the raw payload.
		return raw
	}
}
