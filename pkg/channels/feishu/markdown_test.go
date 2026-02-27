package feishu

import "testing"

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
