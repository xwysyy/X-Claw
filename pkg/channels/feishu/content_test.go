//go:build amd64 || arm64 || riscv64 || mips64 || ppc64

package feishu

import (
	"reflect"
	"testing"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestNormalizeFeishuText_HTMLAndListQuirks(t *testing.T) {
	in := "<p>-\n1</p><p>2.\nsecond</p><p>a&nbsp;&lt;b&gt;</p>"
	got := normalizeFeishuText(in)
	want := "- 1\n2. second\na <b>"
	if got != want {
		t.Fatalf("normalizeFeishuText() = %q, want %q", got, want)
	}
}

func TestNormalizeFeishuText_BRTagsAndWhitespace(t *testing.T) {
	in := "<p>Hello</p><br><br/>world\r\n\r\n\r\nnext"
	got := normalizeFeishuText(in)
	want := "Hello\n\nworld\n\nnext"
	if got != want {
		t.Fatalf("normalizeFeishuText() = %q, want %q", got, want)
	}
}

func TestExtractFeishuPostImageKeys(t *testing.T) {
	raw := `{
		"title":"x",
		"zh_cn": {
			"content": [
				[
					{"tag":"img","image_key":"img_a"},
					{"tag":"text","text":"hello"},
					{"nested":{"image_key":"img_b"}}
				],
				[
					{"tag":"img","image_key":"img_a"}
				]
			]
		}
	}`

	got := extractFeishuPostImageKeys(raw)
	want := []string{"img_a", "img_b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("extractFeishuPostImageKeys() = %#v, want %#v", got, want)
	}
}

func TestExtractFeishuPostImageKeys_CamelCaseAndInvalidJSON(t *testing.T) {
	raw := `{
		"content":[
			{"imageKey":"img_c"},
			{"nested":{"image_key":"img_a"}},
			{"image_key":"img_b"},
			{"imageKey":"img_a"}
		]
	}`
	got := extractFeishuPostImageKeys(raw)
	want := []string{"img_a", "img_b", "img_c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("extractFeishuPostImageKeys() = %#v, want %#v", got, want)
	}

	if got := extractFeishuPostImageKeys("{broken"); got != nil {
		t.Fatalf("extractFeishuPostImageKeys(invalid) = %#v, want nil", got)
	}
}

func TestResolveFeishuFileUploadTypes(t *testing.T) {
	tests := []struct {
		name        string
		mediaType   string
		filename    string
		contentType string
		wantFile    string
		wantMsg     string
	}{
		{
			name:        "video mp4",
			mediaType:   "video",
			filename:    "clip.mp4",
			contentType: "video/mp4",
			wantFile:    "mp4",
			wantMsg:     "media",
		},
		{
			name:        "audio opus",
			mediaType:   "audio",
			filename:    "voice.opus",
			contentType: "audio/opus",
			wantFile:    "opus",
			wantMsg:     "audio",
		},
		{
			name:        "audio non opus fallback to file",
			mediaType:   "audio",
			filename:    "voice.m4a",
			contentType: "audio/mp4",
			wantFile:    "m4a",
			wantMsg:     "file",
		},
		{
			name:        "infer mp4 from content type",
			mediaType:   "video",
			filename:    "clip",
			contentType: "video/mp4",
			wantFile:    "mp4",
			wantMsg:     "media",
		},
		{
			name:        "uppercase extension is normalized",
			mediaType:   "video",
			filename:    "CLIP.MP4",
			contentType: "application/octet-stream",
			wantFile:    "mp4",
			wantMsg:     "media",
		},
		{
			name:        "empty media type keeps file message",
			mediaType:   "",
			filename:    "clip.mp4",
			contentType: "video/mp4",
			wantFile:    "mp4",
			wantMsg:     "file",
		},
		{
			name:        "unknown extension falls back to file type",
			mediaType:   "file",
			filename:    "",
			contentType: "application/octet-stream",
			wantFile:    "file",
			wantMsg:     "file",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotFile, gotMsg := resolveFeishuFileUploadTypes(tc.mediaType, tc.filename, tc.contentType)
			if gotFile != tc.wantFile || gotMsg != tc.wantMsg {
				t.Fatalf("resolveFeishuFileUploadTypes() = (%q, %q), want (%q, %q)", gotFile, gotMsg, tc.wantFile, tc.wantMsg)
			}
		})
	}
}

func TestExtractFeishuMessageContent(t *testing.T) {
	textType := larkim.MsgTypeText
	textPayload := `{"text":"<p>Hello</p><p>-\n1</p>"}`
	msg := &larkim.EventMessage{
		MessageType: &textType,
		Content:     &textPayload,
	}
	if got, want := extractFeishuMessageContent(msg), "Hello\n- 1"; got != want {
		t.Fatalf("extractFeishuMessageContent(text) = %q, want %q", got, want)
	}

	postType := "post"
	postPayload := `{
		"title":"<p>标题</p>",
		"zh_cn":{
			"content":[
				[
					{"tag":"text","text":"hello "},
					{"tag":"a","text":"doc","href":"https://example.com"},
					{"tag":"text","text":" "},
					{"tag":"at","name":"Alice"}
				],
				[
					{"tag":"img","image_key":"img_1"}
				]
			]
		}
	}`
	msg = &larkim.EventMessage{
		MessageType: &postType,
		Content:     &postPayload,
	}
	if got, want := extractFeishuMessageContent(msg), "标题\nhello doc @Alice\n[image]"; got != want {
		t.Fatalf("extractFeishuMessageContent(post) = %q, want %q", got, want)
	}

	badPost := "{bad"
	msg = &larkim.EventMessage{
		MessageType: &postType,
		Content:     &badPost,
	}
	if got := extractFeishuMessageContent(msg); got != badPost {
		t.Fatalf("extractFeishuMessageContent(invalid post) = %q, want raw payload", got)
	}
}

func TestExtractFeishuMessageContent_MediaPlaceholders(t *testing.T) {
	tests := []struct {
		messageType string
		want        string
	}{
		{messageType: "image", want: "<media:image>"},
		{messageType: "file", want: "<media:file>"},
		{messageType: "audio", want: "<media:audio>"},
		{messageType: "video", want: "<media:video>"},
		{messageType: "media", want: "<media:video>"},
		{messageType: "sticker", want: "<media:sticker>"},
	}

	raw := `{"x":"y"}`
	for _, tc := range tests {
		t.Run(tc.messageType, func(t *testing.T) {
			mt := tc.messageType
			msg := &larkim.EventMessage{
				MessageType: &mt,
				Content:     &raw,
			}
			if got := extractFeishuMessageContent(msg); got != tc.want {
				t.Fatalf("extractFeishuMessageContent(%s) = %q, want %q", tc.messageType, got, tc.want)
			}
		})
	}
}

func TestExtractFeishuPostContent_FallbackContentField(t *testing.T) {
	raw := `{
		"title":"<p>t</p>",
		"content":[
			[
				{"tag":"a","href":"https://example.com"},
				{"tag":"text","text":" x"},
				{"tag":"md","text":" y"}
			],
			[
				{"tag":"at","user_name":"Bob"},
				{"tag":"text","text":" hi"},
				{"tag":"img","image_key":"img_1"}
			]
		]
	}`

	got := extractFeishuPostContent(raw)
	want := "t\nhttps://example.com x y\n@Bob hi[image]"
	if got != want {
		t.Fatalf("extractFeishuPostContent() = %q, want %q", got, want)
	}
}

func TestExtractFeishuPostContent_TitleOnlyAndInvalid(t *testing.T) {
	raw := `{"title":"<p>only-title</p>","zh_cn":{"content":[]}}`
	if got, want := extractFeishuPostContent(raw), "only-title"; got != want {
		t.Fatalf("extractFeishuPostContent(title-only) = %q, want %q", got, want)
	}

	if got := extractFeishuPostContent("{bad"); got != "" {
		t.Fatalf("extractFeishuPostContent(invalid) = %q, want empty", got)
	}
}

func TestFeishuExtractJSONString(t *testing.T) {
	raw := `{"file_key":"  ","imageKey":" img_123 "}`
	if got, want := feishuExtractJSONString(raw, "file_key", "imageKey"), "img_123"; got != want {
		t.Fatalf("feishuExtractJSONString() = %q, want %q", got, want)
	}

	if got := feishuExtractJSONString("{bad", "image_key"); got != "" {
		t.Fatalf("feishuExtractJSONString(invalid) = %q, want empty", got)
	}
}

func TestExtractFeishuMessageContent_EmptyAndUnknown(t *testing.T) {
	if got := extractFeishuMessageContent(nil); got != "" {
		t.Fatalf("extractFeishuMessageContent(nil) = %q, want empty", got)
	}

	empty := ""
	msg := &larkim.EventMessage{Content: &empty}
	if got := extractFeishuMessageContent(msg); got != "" {
		t.Fatalf("extractFeishuMessageContent(empty content) = %q, want empty", got)
	}

	unknownType := "custom"
	raw := `{"a":"b"}`
	msg = &larkim.EventMessage{
		MessageType: &unknownType,
		Content:     &raw,
	}
	if got := extractFeishuMessageContent(msg); got != raw {
		t.Fatalf("extractFeishuMessageContent(unknown) = %q, want raw payload", got)
	}

	textType := larkim.MsgTypeText
	invalidText := "{bad"
	msg = &larkim.EventMessage{
		MessageType: &textType,
		Content:     &invalidText,
	}
	if got := extractFeishuMessageContent(msg); got != invalidText {
		t.Fatalf("extractFeishuMessageContent(invalid text json) = %q, want raw payload", got)
	}
}
