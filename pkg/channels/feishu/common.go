package feishu

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// mentionPlaceholderRegex matches @_user_N placeholders inserted by Feishu for mentions.
var mentionPlaceholderRegex = regexp.MustCompile(`@_user_\d+`)

// stringValue safely dereferences a *string pointer.
func stringValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// buildMarkdownCard builds a Feishu Interactive Card JSON 2.0 string with markdown content.
// JSON 2.0 cards support full CommonMark standard markdown syntax.
func buildMarkdownCard(content string) (string, error) {
	card := map[string]any{
		"schema": "2.0",
		"body": map[string]any{
			"elements": []map[string]any{
				{
					"tag":     "markdown",
					"content": content,
				},
			},
		},
	}
	data, err := json.Marshal(card)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// extractJSONStringField unmarshals content as JSON and returns the value of the given string field.
// Returns "" if the content is invalid JSON or the field is missing/empty.
func extractJSONStringField(content, field string) string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &m); err != nil {
		return ""
	}
	raw, ok := m[field]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

// extractImageKey extracts the image_key from a Feishu image message content JSON.
// Format: {"image_key": "img_xxx"}
func extractImageKey(content string) string { return extractJSONStringField(content, "image_key") }

// extractFileKey extracts the file_key from a Feishu file/audio message content JSON.
// Format: {"file_key": "file_xxx", "file_name": "...", ...}
func extractFileKey(content string) string { return extractJSONStringField(content, "file_key") }

// extractFileName extracts the file_name from a Feishu file message content JSON.
func extractFileName(content string) string { return extractJSONStringField(content, "file_name") }

// stripMentionPlaceholders removes @_user_N placeholders from the text content.
// These are inserted by Feishu when users @mention someone in a message.
func stripMentionPlaceholders(content string, mentions []*larkim.MentionEvent) string {
	if len(mentions) == 0 {
		return content
	}
	for _, m := range mentions {
		if m.Key != nil && *m.Key != "" {
			content = strings.ReplaceAll(content, *m.Key, "")
		}
	}
	// Also clean up any remaining @_user_N patterns
	content = mentionPlaceholderRegex.ReplaceAllString(content, "")
	return strings.TrimSpace(content)
}

func sanitizeFeishuUploadFilename(filename string) string {
	filename = strings.TrimSpace(filename)
	if filename == "" {
		return "file"
	}

	// Prevent path traversal and oddities leaking into multipart headers.
	filename = strings.ReplaceAll(filename, "/", "_")
	filename = strings.ReplaceAll(filename, "\\", "_")
	filename = strings.TrimSpace(filename)
	if filename == "" {
		return "file"
	}

	// Remove invalid UTF-8 and control characters (Feishu/lark SDK may choke).
	filename = strings.Map(func(r rune) rune {
		// Drop ASCII control chars. Keep standard whitespace.
		if r < 0x20 && r != '\t' && r != '\n' && r != '\r' {
			return -1
		}
		if r == 0x7f {
			return -1
		}
		return r
	}, filename)
	filename = strings.TrimSpace(filename)
	if filename == "" {
		return "file"
	}
	if !utf8.ValidString(filename) {
		// Best-effort fallback: percent-encode raw bytes.
		return url.PathEscape(string([]byte(filename)))
	}

	// Keep filenames reasonably sized to avoid gigantic multipart headers.
	const maxRunes = 120
	r := []rune(filename)
	if len(r) > maxRunes {
		filename = string(r[:maxRunes])
	}

	// Multipart headers are historically ASCII-hostile; percent-encode when any
	// non-ASCII runes exist to keep uploads reliable in more environments.
	for _, ch := range filename {
		if ch > 0x7f {
			return url.PathEscape(filename)
		}
	}
	return filename
}
