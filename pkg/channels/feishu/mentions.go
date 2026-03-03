package feishu

import (
	"strings"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// feishuUserIDMatches reports whether the Feishu user id object contains the
// given expected identifier (user_id / open_id / union_id).
func feishuUserIDMatches(id *larkim.UserId, expected string) bool {
	expected = strings.TrimSpace(expected)
	if id == nil || expected == "" {
		return false
	}
	if id.UserId != nil && strings.TrimSpace(*id.UserId) == expected {
		return true
	}
	if id.OpenId != nil && strings.TrimSpace(*id.OpenId) == expected {
		return true
	}
	if id.UnionId != nil && strings.TrimSpace(*id.UnionId) == expected {
		return true
	}
	return false
}

// feishuCleanTextMentions:
// - removes bot mentions (when botID is configured)
// - strips leading mentions when botID is unknown (best-effort "mention to trigger")
// - replaces remaining mention placeholders with "@Name" for readability
//
// Returns (cleanedText, mentionedBotOrLeadingMention).
func feishuCleanTextMentions(content string, mentions []*larkim.MentionEvent, botID string) (string, bool) {
	if strings.TrimSpace(content) == "" || len(mentions) == 0 {
		return strings.TrimSpace(content), false
	}

	type mentionInfo struct {
		key      string
		display  string
		isBotHit bool
	}

	infos := make([]mentionInfo, 0, len(mentions))
	keys := make([]string, 0, len(mentions))
	for _, m := range mentions {
		if m == nil || m.Key == nil {
			continue
		}
		key := strings.TrimSpace(*m.Key)
		if key == "" {
			continue
		}
		name := ""
		if m.Name != nil {
			name = strings.TrimSpace(*m.Name)
		}
		display := "@"
		if name != "" {
			display += name
		}
		isBot := false
		if strings.TrimSpace(botID) != "" && m.Id != nil {
			isBot = feishuUserIDMatches(m.Id, botID)
		}

		infos = append(infos, mentionInfo{key: key, display: display, isBotHit: isBot})
		keys = append(keys, key)
	}

	mentioned := false
	out := content

	if strings.TrimSpace(botID) != "" {
		for _, info := range infos {
			if !info.isBotHit {
				continue
			}
			if strings.Contains(out, info.key) {
				mentioned = true
				out = strings.ReplaceAll(out, info.key, "")
			}
		}
	} else {
		// Bot ID unknown: treat leading mention placeholders as trigger and strip them.
		trimmed, removed := feishuStripLeadingMentions(out, keys)
		if removed > 0 {
			mentioned = true
			out = trimmed
		}
	}

	// Replace non-bot (or non-leading-stripped) placeholders with display names.
	for _, info := range infos {
		if strings.TrimSpace(botID) != "" && info.isBotHit {
			continue
		}
		if info.display == "@" { // unknown name: best-effort drop "@"
			continue
		}
		out = strings.ReplaceAll(out, info.key, info.display)
	}

	out = strings.TrimSpace(feishuSpacesRe.ReplaceAllString(out, " "))
	return out, mentioned
}

func feishuStripLeadingMentions(content string, keys []string) (string, int) {
	s := content
	removed := 0
	for {
		s = strings.TrimLeft(s, " \t\r\n")
		if s == "" {
			break
		}
		matched := false
		for _, key := range keys {
			if key == "" {
				continue
			}
			if strings.HasPrefix(s, key) {
				s = s[len(key):]
				removed++
				matched = true
				break
			}
		}
		if !matched {
			break
		}
	}
	return strings.TrimLeft(s, " \t\r\n"), removed
}

// feishuDetectAndStripBotMention is a lightweight mention detector for non-text
// message types (e.g. post). It trims content and returns whether the bot was
// mentioned.
//
// If botID is empty (bot open_id unknown), any mention is treated as a mention hit.
func feishuDetectAndStripBotMention(message *larkim.EventMessage, content string, botID string) (bool, string) {
	cleaned := strings.TrimSpace(content)
	if message == nil || len(message.Mentions) == 0 {
		return false, cleaned
	}
	if strings.TrimSpace(botID) == "" {
		return true, cleaned
	}
	for _, m := range message.Mentions {
		if m == nil || m.Id == nil {
			continue
		}
		if feishuUserIDMatches(m.Id, botID) {
			return true, cleaned
		}
	}
	return false, cleaned
}
