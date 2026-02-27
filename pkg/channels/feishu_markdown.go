package channels

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

const feishuPostChunkLimit = 4000

type feishuPostMarkdownContent struct {
	ZhCN feishuPostMarkdownLocale `json:"zh_cn"`
}

type feishuPostMarkdownLocale struct {
	Content [][]feishuPostMarkdownElement `json:"content"`
}

type feishuPostMarkdownElement struct {
	Tag  string `json:"tag"`
	Text string `json:"text"`
}

func buildFeishuPostMarkdownContent(markdown string) (string, error) {
	payload := feishuPostMarkdownContent{
		ZhCN: feishuPostMarkdownLocale{
			Content: [][]feishuPostMarkdownElement{
				{
					{
						Tag:  "md",
						Text: markdown,
					},
				},
			},
		},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal feishu post markdown content: %w", err)
	}
	return string(encoded), nil
}

var (
	feishuFencedCodeBlockRe = regexp.MustCompile("```[\\s\\S]*?```")
	feishuInlineCodeRe      = regexp.MustCompile("`[^`\\n]*`")
	feishuURLRe             = regexp.MustCompile("https?://[^\\s<>\"'`]+")
	feishuAutoLinkRe        = regexp.MustCompile("<\\s*(https?://[^>\\s]+)\\s*>")
)

// normalizeFeishuMarkdownLinks converts bare URLs into explicit markdown links and
// percent-encodes a small subset of characters that Feishu markdown can mis-handle.
//
// Inspired by: ref/clawdbot-feishu/src/text/markdown-links.ts
func normalizeFeishuMarkdownLinks(text string) string {
	if text == "" {
		return text
	}
	if !strings.Contains(text, "http://") && !strings.Contains(text, "https://") {
		return text
	}

	parts := splitKeepingMatches(feishuFencedCodeBlockRe, text)
	var out strings.Builder
	out.Grow(len(text))
	for _, part := range parts {
		if feishuFencedCodeBlockRe.MatchString(part) && strings.HasPrefix(part, "```") {
			out.WriteString(part)
			continue
		}
		out.WriteString(normalizeFeishuMarkdownLinksNonFenced(part))
	}
	return out.String()
}

func normalizeFeishuMarkdownLinksNonFenced(text string) string {
	parts := splitKeepingMatches(feishuInlineCodeRe, text)
	var out strings.Builder
	out.Grow(len(text))
	for _, part := range parts {
		if feishuInlineCodeRe.MatchString(part) && strings.HasPrefix(part, "`") {
			out.WriteString(part)
			continue
		}
		out.WriteString(wrapBareFeishuURLs(part))
	}
	return out.String()
}

func wrapBareFeishuURLs(text string) string {
	if text == "" {
		return text
	}

	withoutAutoLinks := replaceFeishuAutoLinks(text)
	matches := feishuURLRe.FindAllStringIndex(withoutAutoLinks, -1)
	if len(matches) == 0 {
		return withoutAutoLinks
	}

	var out strings.Builder
	out.Grow(len(withoutAutoLinks))

	last := 0
	for _, match := range matches {
		start, end := match[0], match[1]
		if start < last {
			continue
		}

		out.WriteString(withoutAutoLinks[last:start])

		rawURL := withoutAutoLinks[start:end]
		url, trailing := splitTrailingPunctuation(rawURL)
		if url == "" {
			out.WriteString(rawURL)
			last = end
			continue
		}

		isMarkdownDestination := start >= 2 && withoutAutoLinks[start-2:start] == "]("
		isMarkdownLinkLabelURL := start > 0 && withoutAutoLinks[start-1] == '[' &&
			end+2 <= len(withoutAutoLinks) && withoutAutoLinks[end:end+2] == "]("

		normalizedURL := normalizeURLForFeishu(url)
		if isMarkdownDestination || isMarkdownLinkLabelURL {
			out.WriteString(normalizedURL)
			out.WriteString(trailing)
			last = end
			continue
		}

		out.WriteString(buildMarkdownLink(normalizedURL))
		out.WriteString(trailing)
		last = end
	}

	out.WriteString(withoutAutoLinks[last:])
	return out.String()
}

func replaceFeishuAutoLinks(text string) string {
	indices := feishuAutoLinkRe.FindAllStringSubmatchIndex(text, -1)
	if len(indices) == 0 {
		return text
	}

	var out strings.Builder
	out.Grow(len(text))
	last := 0
	for _, idx := range indices {
		if len(idx) < 4 {
			continue
		}

		fullStart, fullEnd := idx[0], idx[1]
		urlStart, urlEnd := idx[2], idx[3]
		if fullStart < last || urlStart < 0 || urlEnd < 0 {
			continue
		}

		out.WriteString(text[last:fullStart])
		out.WriteString(normalizeURLForFeishu(text[urlStart:urlEnd]))
		last = fullEnd
	}
	out.WriteString(text[last:])
	return out.String()
}

func buildMarkdownLink(url string) string {
	label := strings.ReplaceAll(url, "[", "\\[")
	label = strings.ReplaceAll(label, "]", "\\]")
	return fmt.Sprintf("[%s](%s)", label, url)
}

func normalizeURLForFeishu(url string) string {
	return strings.NewReplacer(
		"_", "%5F",
		"(", "%28",
		")", "%29",
	).Replace(url)
}

func splitTrailingPunctuation(rawURL string) (url string, trailing string) {
	url = rawURL
	trailing = ""
	open, close := countParens(rawURL)

	for url != "" {
		runes := []rune(url)
		if len(runes) == 0 {
			break
		}
		tail := runes[len(runes)-1]
		closeParenOverflow := tail == ')' && close > open
		if !isTrailingPunct(tail) && !closeParenOverflow {
			break
		}
		if tail == ')' {
			close--
		}
		trailing = string(tail) + trailing
		url = string(runes[:len(runes)-1])
	}

	return url, trailing
}

func countParens(text string) (open int, close int) {
	for _, r := range text {
		switch r {
		case '(':
			open++
		case ')':
			close++
		}
	}
	return open, close
}

func isTrailingPunct(r rune) bool {
	switch r {
	case '.', ',', ';', '!', '?', '。', '，', '；', '！', '？', '、':
		return true
	default:
		return false
	}
}

func splitKeepingMatches(re *regexp.Regexp, s string) []string {
	matches := re.FindAllStringIndex(s, -1)
	if len(matches) == 0 {
		return []string{s}
	}

	parts := make([]string, 0, len(matches)*2+1)
	last := 0
	for _, m := range matches {
		start, end := m[0], m[1]
		if start > last {
			parts = append(parts, s[last:start])
		}
		parts = append(parts, s[start:end])
		last = end
	}
	if last < len(s) {
		parts = append(parts, s[last:])
	}
	return parts
}
