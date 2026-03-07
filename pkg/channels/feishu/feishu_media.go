//go:build amd64 || arm64 || riscv64 || mips64 || ppc64

package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"

	"github.com/xwysyy/X-Claw/pkg/bus"
	"github.com/xwysyy/X-Claw/pkg/channels"
	"github.com/xwysyy/X-Claw/pkg/logger"
	"github.com/xwysyy/X-Claw/pkg/media"
	"github.com/xwysyy/X-Claw/pkg/utils"
)

func (c *FeishuChannel) SendMedia(ctx context.Context, msg bus.OutboundMediaMessage) error {
	if !c.IsRunning() {
		return channels.ErrNotRunning
	}

	if msg.ChatID == "" {
		return fmt.Errorf("chat ID is empty: %w", channels.ErrSendFailed)
	}

	store := c.GetMediaStore()
	if store == nil {
		return fmt.Errorf("no media store available: %w", channels.ErrSendFailed)
	}

	for _, part := range msg.Parts {
		if err := c.sendMediaPart(ctx, msg.ChatID, part, store); err != nil {
			return err
		}
		if strings.TrimSpace(part.Caption) != "" {
			if err := c.Send(ctx, bus.OutboundMessage{
				Channel: "feishu",
				ChatID:  msg.ChatID,
				Content: part.Caption,
			}); err != nil {
				return err
			}
		}
	}

	return nil
}

// sendMediaPart resolves and sends a single media part.
func (c *FeishuChannel) sendMediaPart(
	ctx context.Context,
	chatID string,
	part bus.MediaPart,
	store media.MediaStore,
) error {
	localPath, err := store.Resolve(part.Ref)
	if err != nil {
		logger.ErrorCF("feishu", "Failed to resolve media ref", map[string]any{
			"ref":   part.Ref,
			"error": err.Error(),
		})
		return nil // skip this part
	}

	file, err := os.Open(localPath)
	if err != nil {
		logger.ErrorCF("feishu", "Failed to open media file", map[string]any{
			"path":  localPath,
			"error": err.Error(),
		})
		return nil // skip this part
	}
	defer file.Close()

	switch part.Type {
	case "image":
		err = c.sendImage(ctx, chatID, file)
	default:
		filename := part.Filename
		if filename == "" {
			filename = "file"
		}
		err = c.sendFile(ctx, chatID, file, filename, part.Type)
	}

	if err != nil {
		logger.ErrorCF("feishu", "Failed to send media", map[string]any{
			"type":  part.Type,
			"error": err.Error(),
		})
		return fmt.Errorf("feishu send media: %w", channels.ErrTemporary)
	}
	return nil
}

// --- Inbound message handling ---

func (c *FeishuChannel) downloadInboundMedia(
	ctx context.Context,
	chatID, messageID, messageType, rawContent string,
	store media.MediaStore,
) []string {
	var refs []string
	scope := channels.BuildMediaScope("feishu", chatID, messageID)

	switch messageType {
	case larkim.MsgTypeImage:
		imageKey := extractImageKey(rawContent)
		if imageKey == "" {
			return nil
		}
		ref := c.downloadResource(ctx, messageID, imageKey, "image", ".jpg", store, scope)
		if ref != "" {
			refs = append(refs, ref)
		}

	case larkim.MsgTypeFile, larkim.MsgTypeAudio, larkim.MsgTypeMedia:
		fileKey := extractFileKey(rawContent)
		if fileKey == "" {
			return nil
		}
		// Derive a fallback extension from the message type.
		var ext string
		switch messageType {
		case larkim.MsgTypeAudio:
			ext = ".ogg"
		case larkim.MsgTypeMedia:
			ext = ".mp4"
		default:
			ext = "" // generic file — rely on resp.FileName
		}
		ref := c.downloadResource(ctx, messageID, fileKey, "file", ext, store, scope)
		if ref != "" {
			refs = append(refs, ref)
		}
	}

	return refs
}

// downloadResource downloads a message resource (image/file) from Feishu,
// writes it to the project media directory, and stores the reference in MediaStore.
// fallbackExt (e.g. ".jpg") is appended when the resolved filename has no extension.
func (c *FeishuChannel) downloadResource(
	ctx context.Context,
	messageID, fileKey, resourceType, fallbackExt string,
	store media.MediaStore,
	scope string,
) string {
	req := larkim.NewGetMessageResourceReqBuilder().
		MessageId(messageID).
		FileKey(fileKey).
		Type(resourceType).
		Build()

	resp, err := c.client.Im.V1.MessageResource.Get(ctx, req)
	if err != nil {
		logger.ErrorCF("feishu", "Failed to download resource", map[string]any{
			"message_id": messageID,
			"file_key":   fileKey,
			"error":      err.Error(),
		})
		return ""
	}
	if !resp.Success() {
		logger.ErrorCF("feishu", "Resource download api error", map[string]any{
			"code": resp.Code,
			"msg":  resp.Msg,
		})
		return ""
	}

	if resp.File == nil {
		return ""
	}
	// Safely close the underlying reader if it implements io.Closer (e.g. HTTP response body).
	if closer, ok := resp.File.(io.Closer); ok {
		defer closer.Close()
	}

	filename := resp.FileName
	if filename == "" {
		filename = fileKey
	}
	// If filename still has no extension, append the fallback (like Telegram's ext parameter).
	if filepath.Ext(filename) == "" && fallbackExt != "" {
		filename += fallbackExt
	}

	// Write to the shared media temp directory using a unique name to avoid collisions.
	mediaDir := utils.MediaTempDir()
	if mkdirErr := os.MkdirAll(mediaDir, 0o700); mkdirErr != nil {
		logger.ErrorCF("feishu", "Failed to create media directory", map[string]any{
			"error": mkdirErr.Error(),
		})
		return ""
	}
	ext := filepath.Ext(filename)
	localPath := filepath.Join(mediaDir, utils.SanitizeFilename(messageID+"-"+fileKey+ext))

	out, err := os.Create(localPath)
	if err != nil {
		logger.ErrorCF("feishu", "Failed to create local file for resource", map[string]any{
			"error": err.Error(),
		})
		return ""
	}

	if _, copyErr := io.Copy(out, resp.File); copyErr != nil {
		out.Close()
		os.Remove(localPath)
		logger.ErrorCF("feishu", "Failed to write resource to file", map[string]any{
			"error": copyErr.Error(),
		})
		return ""
	}
	out.Close()

	ref, err := store.Store(localPath, media.MediaMeta{
		Filename: filename,
		Source:   "feishu",
	}, scope)
	if err != nil {
		logger.ErrorCF("feishu", "Failed to store downloaded resource", map[string]any{
			"file_key": fileKey,
			"error":    err.Error(),
		})
		os.Remove(localPath)
		return ""
	}

	return ref
}

// appendMediaTags appends media type tags to content (like Telegram's "[image: photo]").

func (c *FeishuChannel) sendImage(ctx context.Context, chatID string, file *os.File) error {
	// Upload image to get image_key
	uploadReq := larkim.NewCreateImageReqBuilder().
		Body(larkim.NewCreateImageReqBodyBuilder().
			ImageType("message").
			Image(file).
			Build()).
		Build()

	uploadResp, err := c.client.Im.V1.Image.Create(ctx, uploadReq)
	if err != nil {
		return fmt.Errorf("feishu image upload: %w", err)
	}
	if !uploadResp.Success() {
		return fmt.Errorf("feishu image upload api error (code=%d msg=%s)", uploadResp.Code, uploadResp.Msg)
	}
	if uploadResp.Data == nil || uploadResp.Data.ImageKey == nil {
		return fmt.Errorf("feishu image upload: no image_key returned")
	}

	imageKey := *uploadResp.Data.ImageKey

	// Send image message
	content, _ := json.Marshal(map[string]string{"image_key": imageKey})
	req := larkim.NewCreateMessageReqBuilder().
		ReceiveIdType(larkim.ReceiveIdTypeChatId).
		Body(larkim.NewCreateMessageReqBodyBuilder().
			ReceiveId(chatID).
			MsgType(larkim.MsgTypeImage).
			Content(string(content)).
			Build()).
		Build()

	resp, err := c.client.Im.V1.Message.Create(ctx, req)
	if err != nil {
		return fmt.Errorf("feishu image send: %w", err)
	}
	if !resp.Success() {
		return fmt.Errorf("feishu image send api error (code=%d msg=%s)", resp.Code, resp.Msg)
	}
	return nil
}

// sendFile uploads a file and sends it as a message.
func (c *FeishuChannel) sendFile(ctx context.Context, chatID string, file *os.File, filename, fileType string) error {
	filename = sanitizeFeishuUploadFilename(filename)

	// Map part type to Feishu file type
	feishuFileType := "stream"
	switch fileType {
	case "audio":
		feishuFileType = "opus"
	case "video":
		feishuFileType = "mp4"
	}

	// Upload file to get file_key
	uploadReq := larkim.NewCreateFileReqBuilder().
		Body(larkim.NewCreateFileReqBodyBuilder().
			FileType(feishuFileType).
			FileName(filename).
			File(file).
			Build()).
		Build()

	uploadResp, err := c.client.Im.V1.File.Create(ctx, uploadReq)
	if err != nil {
		return fmt.Errorf("feishu file upload: %w", err)
	}
	if !uploadResp.Success() {
		return fmt.Errorf("feishu file upload api error (code=%d msg=%s)", uploadResp.Code, uploadResp.Msg)
	}
	if uploadResp.Data == nil || uploadResp.Data.FileKey == nil {
		return fmt.Errorf("feishu file upload: no file_key returned")
	}

	fileKey := *uploadResp.Data.FileKey

	// Send file message
	content, _ := json.Marshal(map[string]string{"file_key": fileKey})
	req := larkim.NewCreateMessageReqBuilder().
		ReceiveIdType(larkim.ReceiveIdTypeChatId).
		Body(larkim.NewCreateMessageReqBodyBuilder().
			ReceiveId(chatID).
			MsgType(larkim.MsgTypeFile).
			Content(string(content)).
			Build()).
		Build()

	resp, err := c.client.Im.V1.Message.Create(ctx, req)
	if err != nil {
		return fmt.Errorf("feishu file send: %w", err)
	}
	if !resp.Success() {
		return fmt.Errorf("feishu file send api error (code=%d msg=%s)", resp.Code, resp.Msg)
	}
	return nil
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

func resolveFeishuFileUploadTypes(mediaType, filename, contentType string) (fileType string, messageType string) {
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	contentType = strings.ToLower(strings.TrimSpace(contentType))

	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(strings.TrimSpace(filename)), "."))
	if ext == "" {
		detectedType := channels.MediaTypeFromMIME(contentType)
		if detectedType == "video" || detectedType == "audio" {
			parts := strings.SplitN(contentType, "/", 2)
			if len(parts) == 2 {
				sub := strings.TrimSpace(parts[1])
				// Avoid overly generic values like "application/octet-stream".
				if sub != "" && sub != "octet-stream" {
					ext = sub
				}
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
