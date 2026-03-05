// X-Claw - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 X-Claw contributors

package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/h2non/filetype"

	"github.com/xwysyy/X-Claw/pkg/bus"
	"github.com/xwysyy/X-Claw/pkg/logger"
	"github.com/xwysyy/X-Claw/pkg/providers"
	"github.com/xwysyy/X-Claw/pkg/utils"
)

// resolveMediaRefs replaces media:// refs in message Media fields with base64 data URLs.
// Uses streaming base64 encoding (file handle → encoder → buffer) to avoid holding
// both raw bytes and encoded string in memory simultaneously.
// Returns a new slice; original messages are not mutated.
func resolveMediaRefs(messages []providers.Message, resolver MediaResolver, maxSize int) []providers.Message {
	if resolver == nil {
		return messages
	}

	result := make([]providers.Message, len(messages))
	copy(result, messages)

	for i, m := range result {
		if len(m.Media) == 0 {
			continue
		}

		resolved := make([]string, 0, len(m.Media))
		for _, ref := range m.Media {
			if !strings.HasPrefix(ref, "media://") {
				resolved = append(resolved, ref)
				continue
			}

			localPath, meta, err := resolver.ResolveWithMeta(ref)
			if err != nil {
				logger.WarnCF("agent", "Failed to resolve media ref", map[string]any{
					"ref":   ref,
					"error": err.Error(),
				})
				continue
			}

			info, err := os.Stat(localPath)
			if err != nil {
				logger.WarnCF("agent", "Failed to stat media file", map[string]any{
					"path":  localPath,
					"error": err.Error(),
				})
				continue
			}
			if info.Size() > int64(maxSize) {
				logger.WarnCF("agent", "Media file too large, skipping", map[string]any{
					"path":     localPath,
					"size":     info.Size(),
					"max_size": maxSize,
				})
				continue
			}

			// Determine MIME type: prefer metadata, fallback to magic-bytes detection
			mime := meta.ContentType
			if mime == "" {
				kind, ftErr := filetype.MatchFile(localPath)
				if ftErr != nil || kind == filetype.Unknown {
					logger.WarnCF("agent", "Unknown media type, skipping", map[string]any{
						"path": localPath,
					})
					continue
				}
				mime = kind.MIME.Value
			}

			// Streaming base64: open file → base64 encoder → buffer
			// Peak memory: ~1.33x file size (buffer only, no raw bytes copy)
			f, err := os.Open(localPath)
			if err != nil {
				logger.WarnCF("agent", "Failed to open media file", map[string]any{
					"path":  localPath,
					"error": err.Error(),
				})
				continue
			}

			prefix := "data:" + mime + ";base64,"
			encodedLen := base64.StdEncoding.EncodedLen(int(info.Size()))
			var buf bytes.Buffer
			buf.Grow(len(prefix) + encodedLen)
			buf.WriteString(prefix)

			encoder := base64.NewEncoder(base64.StdEncoding, &buf)
			if _, err := io.Copy(encoder, f); err != nil {
				f.Close()
				logger.WarnCF("agent", "Failed to encode media file", map[string]any{
					"path":  localPath,
					"error": err.Error(),
				})
				continue
			}
			encoder.Close()
			f.Close()

			resolved = append(resolved, buf.String())
		}

		result[i].Media = resolved
	}

	return result
}

func (al *AgentLoop) transcribeAudioInMessage(ctx context.Context, msg bus.InboundMessage) bus.InboundMessage {
	if al == nil || al.transcriber == nil || al.mediaResolver == nil || len(msg.Media) == 0 {
		return msg
	}

	transcriptions := make([]string, 0, len(msg.Media))
	for _, ref := range msg.Media {
		path, meta, err := al.mediaResolver.ResolveWithMeta(ref)
		if err != nil {
			logger.WarnCF("voice", "Failed to resolve media ref", map[string]any{"ref": ref, "error": err})
			continue
		}
		if !utils.IsAudioFile(meta.Filename, meta.ContentType) {
			continue
		}
		result, err := al.transcriber.Transcribe(ctx, path)
		if err != nil {
			logger.WarnCF("voice", "Transcription failed", map[string]any{"ref": ref, "error": err})
			transcriptions = append(transcriptions, "(transcription failed)")
			continue
		}
		transcriptions = append(transcriptions, strings.TrimSpace(result.Text))
	}

	if len(transcriptions) == 0 {
		return msg
	}

	idx := 0
	newContent := audioAnnotationRe.ReplaceAllStringFunc(msg.Content, func(match string) string {
		if idx >= len(transcriptions) {
			return match
		}
		text := transcriptions[idx]
		idx++
		if text == "" {
			return match
		}
		return "[voice: " + text + "]"
	})

	for ; idx < len(transcriptions); idx++ {
		text := strings.TrimSpace(transcriptions[idx])
		if text == "" {
			continue
		}
		newContent += "\n[voice: " + text + "]"
	}

	msg.Content = newContent
	return msg
}

func inferMediaType(filename, contentType string) string {
	ct := strings.ToLower(contentType)
	fn := strings.ToLower(filename)

	if strings.HasPrefix(ct, "image/") {
		return "image"
	}
	if strings.HasPrefix(ct, "audio/") || ct == "application/ogg" {
		return "audio"
	}
	if strings.HasPrefix(ct, "video/") {
		return "video"
	}

	ext := filepath.Ext(fn)
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".svg":
		return "image"
	case ".mp3", ".wav", ".ogg", ".m4a", ".flac", ".aac", ".wma", ".opus":
		return "audio"
	case ".mp4", ".avi", ".mov", ".webm", ".mkv":
		return "video"
	}

	return "file"
}
