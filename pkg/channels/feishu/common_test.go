package feishu

import (
	"encoding/json"
	"net/url"
	"testing"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestExtractJSONStringField(t *testing.T) {
	tests := []struct {
		name    string
		content string
		field   string
		want    string
	}{
		{
			name:    "valid field",
			content: `{"image_key": "img_v2_xxx"}`,
			field:   "image_key",
			want:    "img_v2_xxx",
		},
		{
			name:    "missing field",
			content: `{"image_key": "img_v2_xxx"}`,
			field:   "file_key",
			want:    "",
		},
		{
			name:    "invalid JSON",
			content: `not json at all`,
			field:   "image_key",
			want:    "",
		},
		{
			name:    "empty content",
			content: "",
			field:   "image_key",
			want:    "",
		},
		{
			name:    "non-string field value",
			content: `{"count": 42}`,
			field:   "count",
			want:    "",
		},
		{
			name:    "empty string value",
			content: `{"image_key": ""}`,
			field:   "image_key",
			want:    "",
		},
		{
			name:    "multiple fields",
			content: `{"file_key": "file_xxx", "file_name": "test.pdf"}`,
			field:   "file_name",
			want:    "test.pdf",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractJSONStringField(tt.content, tt.field)
			if got != tt.want {
				t.Errorf("extractJSONStringField(%q, %q) = %q, want %q", tt.content, tt.field, got, tt.want)
			}
		})
	}
}

func TestExtractImageKey(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "normal",
			content: `{"image_key": "img_v2_abc123"}`,
			want:    "img_v2_abc123",
		},
		{
			name:    "missing key",
			content: `{"file_key": "file_xxx"}`,
			want:    "",
		},
		{
			name:    "malformed JSON",
			content: `{broken`,
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractImageKey(tt.content)
			if got != tt.want {
				t.Errorf("extractImageKey(%q) = %q, want %q", tt.content, got, tt.want)
			}
		})
	}
}

func TestExtractFileKey(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "normal",
			content: `{"file_key": "file_v2_abc123", "file_name": "test.doc"}`,
			want:    "file_v2_abc123",
		},
		{
			name:    "missing key",
			content: `{"image_key": "img_xxx"}`,
			want:    "",
		},
		{
			name:    "malformed JSON",
			content: `not json`,
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractFileKey(tt.content)
			if got != tt.want {
				t.Errorf("extractFileKey(%q) = %q, want %q", tt.content, got, tt.want)
			}
		})
	}
}

func TestExtractFileName(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "normal",
			content: `{"file_key": "file_xxx", "file_name": "report.pdf"}`,
			want:    "report.pdf",
		},
		{
			name:    "missing name",
			content: `{"file_key": "file_xxx"}`,
			want:    "",
		},
		{
			name:    "malformed JSON",
			content: `{bad`,
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractFileName(tt.content)
			if got != tt.want {
				t.Errorf("extractFileName(%q) = %q, want %q", tt.content, got, tt.want)
			}
		})
	}
}

func TestBuildMarkdownCard(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{
			name:    "normal content",
			content: "Hello **world**",
		},
		{
			name:    "empty content",
			content: "",
		},
		{
			name:    "special characters",
			content: `Code: "foo" & <bar> 'baz'`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := buildMarkdownCard(tt.content)
			if err != nil {
				t.Fatalf("buildMarkdownCard(%q) unexpected error: %v", tt.content, err)
			}

			// Verify valid JSON
			var parsed map[string]any
			if err := json.Unmarshal([]byte(result), &parsed); err != nil {
				t.Fatalf("buildMarkdownCard(%q) produced invalid JSON: %v", tt.content, err)
			}

			// Verify schema
			if parsed["schema"] != "2.0" {
				t.Errorf("schema = %v, want %q", parsed["schema"], "2.0")
			}

			// Verify body.elements[0].content == input
			body, ok := parsed["body"].(map[string]any)
			if !ok {
				t.Fatal("missing body in card JSON")
			}
			elements, ok := body["elements"].([]any)
			if !ok || len(elements) == 0 {
				t.Fatal("missing or empty elements in card JSON")
			}
			elem, ok := elements[0].(map[string]any)
			if !ok {
				t.Fatal("first element is not an object")
			}
			if elem["tag"] != "markdown" {
				t.Errorf("tag = %v, want %q", elem["tag"], "markdown")
			}
			if elem["content"] != tt.content {
				t.Errorf("content = %v, want %q", elem["content"], tt.content)
			}
		})
	}
}

func TestStripMentionPlaceholders(t *testing.T) {
	strPtr := func(s string) *string { return &s }

	tests := []struct {
		name     string
		content  string
		mentions []*larkim.MentionEvent
		want     string
	}{
		{
			name:     "no mentions",
			content:  "Hello world",
			mentions: nil,
			want:     "Hello world",
		},
		{
			name:    "single mention",
			content: "@_user_1 hello",
			mentions: []*larkim.MentionEvent{
				{Key: strPtr("@_user_1")},
			},
			want: "hello",
		},
		{
			name:    "multiple mentions",
			content: "@_user_1 @_user_2 hey",
			mentions: []*larkim.MentionEvent{
				{Key: strPtr("@_user_1")},
				{Key: strPtr("@_user_2")},
			},
			want: "hey",
		},
		{
			name:     "empty content",
			content:  "",
			mentions: []*larkim.MentionEvent{{Key: strPtr("@_user_1")}},
			want:     "",
		},
		{
			name:     "empty mentions slice",
			content:  "@_user_1 test",
			mentions: []*larkim.MentionEvent{},
			want:     "@_user_1 test",
		},
		{
			name:    "mention with nil key",
			content: "@_user_1 test",
			mentions: []*larkim.MentionEvent{
				{Key: nil},
			},
			want: "test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripMentionPlaceholders(tt.content, tt.mentions)
			if got != tt.want {
				t.Errorf("stripMentionPlaceholders(%q, ...) = %q, want %q", tt.content, got, tt.want)
			}
		})
	}
}

func TestSanitizeFeishuUploadFilename(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: "file"},
		{name: "ascii", in: "report-final.pdf", want: "report-final.pdf"},
		{name: "path separators", in: "a/b\\c.txt", want: "a_b_c.txt"},
		{name: "control chars dropped", in: "a\u0007b.txt", want: "ab.txt"},
		{name: "non-ascii encoded", in: "报告(最终).pdf", want: url.PathEscape("报告(最终).pdf")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeFeishuUploadFilename(tt.in)
			if got != tt.want {
				t.Fatalf("sanitizeFeishuUploadFilename(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestFeishuCleanTextMentions_WithBotID_StripsBotAndReplacesOthers(t *testing.T) {
	botKey := "@_user_1"
	botName := "X-Claw"
	botOpenID := "ou_bot_123"

	userKey := "@_user_2"
	userName := "Alice"
	userOpenID := "ou_user_456"

	mentions := []*larkim.MentionEvent{
		{
			Key:  &botKey,
			Name: &botName,
			Id:   &larkim.UserId{OpenId: &botOpenID},
		},
		{
			Key:  &userKey,
			Name: &userName,
			Id:   &larkim.UserId{OpenId: &userOpenID},
		},
	}

	cleaned, mentioned := feishuCleanTextMentions(botKey+" hello "+userKey, mentions, botOpenID)
	if !mentioned {
		t.Fatalf("expected mentioned=true")
	}
	if cleaned != "hello @Alice" {
		t.Fatalf("unexpected cleaned content: %q", cleaned)
	}
}

func TestFeishuCleanTextMentions_WithoutBotID_OnlyStripsLeadingMention(t *testing.T) {
	key := "@_user_1"
	name := "Someone"
	openID := "ou_someone"

	mentions := []*larkim.MentionEvent{
		{
			Key:  &key,
			Name: &name,
			Id:   &larkim.UserId{OpenId: &openID},
		},
	}

	cleaned, mentioned := feishuCleanTextMentions(key+" hello", mentions, "")
	if !mentioned {
		t.Fatalf("expected mentioned=true for leading mention without bot_id")
	}
	if cleaned != "hello" {
		t.Fatalf("unexpected cleaned content: %q", cleaned)
	}

	cleaned, mentioned = feishuCleanTextMentions("hello "+key, mentions, "")
	if mentioned {
		t.Fatalf("expected mentioned=false for mid-text mention without bot_id")
	}
	if cleaned != "hello @Someone" {
		t.Fatalf("unexpected cleaned content: %q", cleaned)
	}
}

func TestFeishuCleanTextMentions_WithoutBotID_StripsMultipleLeadingMentions(t *testing.T) {
	k1 := "@_user_1"
	n1 := "A"
	id1 := "ou_a"
	k2 := "@_user_2"
	n2 := "B"
	id2 := "ou_b"

	mentions := []*larkim.MentionEvent{
		{Key: &k1, Name: &n1, Id: &larkim.UserId{OpenId: &id1}},
		{Key: &k2, Name: &n2, Id: &larkim.UserId{OpenId: &id2}},
	}

	cleaned, mentioned := feishuCleanTextMentions(k1+" "+k2+" hi", mentions, "")
	if !mentioned {
		t.Fatalf("expected mentioned=true")
	}
	if cleaned != "hi" {
		t.Fatalf("unexpected cleaned content: %q", cleaned)
	}
}

func TestFeishuDetectAndStripBotMention_NonTextMessages(t *testing.T) {
	key := "@_user_1"
	name := "Bot"
	openID := "ou_bot_123"

	mentions := []*larkim.MentionEvent{
		{
			Key:  &key,
			Name: &name,
			Id:   &larkim.UserId{OpenId: &openID},
		},
	}

	msgType := "post"
	message := &larkim.EventMessage{
		MessageType: &msgType,
		Mentions:    mentions,
	}

	mentioned, cleaned := feishuDetectAndStripBotMention(message, " hello ", "")
	if !mentioned {
		t.Fatalf("expected mentioned=true when bot_id is empty and mentions exist")
	}
	if cleaned != "hello" {
		t.Fatalf("unexpected cleaned content: %q", cleaned)
	}

	mentioned, cleaned = feishuDetectAndStripBotMention(message, " hi ", openID)
	if !mentioned {
		t.Fatalf("expected mentioned=true when mention matches bot_id")
	}
	if cleaned != "hi" {
		t.Fatalf("unexpected cleaned content: %q", cleaned)
	}

	mentioned, cleaned = feishuDetectAndStripBotMention(message, "hi", "ou_other")
	if mentioned {
		t.Fatalf("expected mentioned=false when no mention matches configured bot_id")
	}
	if cleaned != "hi" {
		t.Fatalf("unexpected cleaned content: %q", cleaned)
	}
}

func TestFeishuUserIDMatches(t *testing.T) {
	userID := "u_1"
	openID := "ou_1"
	unionID := "un_1"
	id := &larkim.UserId{
		UserId:  &userID,
		OpenId:  &openID,
		UnionId: &unionID,
	}

	if !feishuUserIDMatches(id, userID) {
		t.Fatalf("expected user_id match")
	}
	if !feishuUserIDMatches(id, openID) {
		t.Fatalf("expected open_id match")
	}
	if !feishuUserIDMatches(id, unionID) {
		t.Fatalf("expected union_id match")
	}
	if feishuUserIDMatches(id, "other") {
		t.Fatalf("did not expect mismatch to return true")
	}
	if feishuUserIDMatches(nil, openID) {
		t.Fatalf("nil id should not match")
	}
	if feishuUserIDMatches(id, "") {
		t.Fatalf("empty expected value should not match")
	}
}

func TestNormalizeFeishuMarkdownLinks(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "wraps bare url",
			in:   "see https://example.com/a_b",
			want: "see [https://example.com/a%5Fb](https://example.com/a%5Fb)",
		},
		{
			name: "normalizes markdown destination only",
			in:   "[x](https://example.com/a_b)",
			want: "[x](https://example.com/a%5Fb)",
		},
		{
			name: "converts auto link",
			in:   "<https://example.com/a_b>",
			want: "[https://example.com/a%5Fb](https://example.com/a%5Fb)",
		},
		{
			name: "preserves fenced code blocks",
			in:   "```txt\nsee https://example.com/a_b\n```",
			want: "```txt\nsee https://example.com/a_b\n```",
		},
		{
			name: "preserves inline code",
			in:   "use `https://example.com/a_b` please",
			want: "use `https://example.com/a_b` please",
		},
		{
			name: "keeps trailing punctuation outside link",
			in:   "visit https://example.com/a_b).",
			want: "visit [https://example.com/a%5Fb](https://example.com/a%5Fb)).",
		},
		{
			name: "encodes parentheses inside url",
			in:   "https://example.com/a(b)",
			want: "[https://example.com/a%28b%29](https://example.com/a%28b%29)",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeFeishuMarkdownLinks(tc.in)
			if got != tc.want {
				t.Fatalf("normalizeFeishuMarkdownLinks() = %q, want %q", got, tc.want)
			}
		})
	}
}
