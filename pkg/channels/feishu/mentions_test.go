//go:build amd64 || arm64 || riscv64 || mips64 || ppc64

package feishu

import (
	"testing"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestFeishuCleanTextMentions_WithBotID_StripsBotAndReplacesOthers(t *testing.T) {
	botKey := "@_user_1"
	botName := "PicoClaw"
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
