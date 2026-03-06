package httpapi

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/xwysyy/X-Claw/pkg/utils"
)

type traceListOptions struct {
	kind      string
	baseDir   string
	eventsRel func(token string) string
}

type traceSessionItem struct {
	Token      string `json:"token"`
	Kind       string `json:"kind"`
	SessionKey string `json:"session_key,omitempty"`
	Channel    string `json:"channel,omitempty"`
	ChatID     string `json:"chat_id,omitempty"`
	RunID      string `json:"run_id,omitempty"`

	EventsPath string `json:"events_path"`
	EventsSize int64  `json:"events_size_bytes,omitempty"`
	ModTime    string `json:"mod_time,omitempty"`

	LastEventType string `json:"last_event_type,omitempty"`
	LastEventTS   string `json:"last_event_ts,omitempty"`
	LastEventTSMS int64  `json:"last_event_ts_ms,omitempty"`
}

func (h *ConsoleHandler) handleTraceList(w http.ResponseWriter, _ *http.Request, opts traceListOptions) {
	items, err := listTraceSessions(opts)
	if err != nil {
		h.writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"ok":    true,
		"kind":  opts.kind,
		"items": items,
	})
}

func listTraceSessions(opts traceListOptions) ([]traceSessionItem, error) {
	entries, err := os.ReadDir(opts.baseDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []traceSessionItem{}, nil
		}
		return nil, err
	}

	items := make([]traceSessionItem, 0, len(entries))
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		token := ent.Name()
		if token == "" {
			continue
		}
		eventsPath := filepath.Join(opts.baseDir, token, "events.jsonl")
		st, err := os.Stat(eventsPath)
		if err != nil {
			continue
		}

		sessionKey, channel, chatID, runID := "", "", "", ""
		if line, err := readFirstNonEmptyLine(eventsPath, 64<<10); err == nil {
			var meta struct {
				SessionKey string `json:"session_key"`
				Channel    string `json:"channel"`
				ChatID     string `json:"chat_id"`
				RunID      string `json:"run_id"`
			}
			if json.Unmarshal([]byte(line), &meta) == nil {
				sessionKey = utils.CanonicalSessionKey(meta.SessionKey)
				channel = strings.TrimSpace(meta.Channel)
				chatID = strings.TrimSpace(meta.ChatID)
				runID = strings.TrimSpace(meta.RunID)
			}
		}

		lastType, lastTS, lastTSMS := "", "", int64(0)
		if line, err := readLastNonEmptyLine(eventsPath, 64<<10); err == nil {
			var meta struct {
				Type string `json:"type"`
				TS   string `json:"ts"`
				TSMS int64  `json:"ts_ms"`
			}
			if json.Unmarshal([]byte(line), &meta) == nil {
				lastType = strings.TrimSpace(meta.Type)
				lastTS = strings.TrimSpace(meta.TS)
				lastTSMS = meta.TSMS
			}
		}

		items = append(items, traceSessionItem{
			Token:      token,
			Kind:       opts.kind,
			SessionKey: sessionKey,
			Channel:    channel,
			ChatID:     chatID,
			RunID:      runID,

			EventsPath: opts.eventsRel(token),
			EventsSize: st.Size(),
			ModTime:    st.ModTime().UTC().Format(time.RFC3339Nano),

			LastEventType: lastType,
			LastEventTS:   lastTS,
			LastEventTSMS: lastTSMS,
		})
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].ModTime == items[j].ModTime {
			return items[i].Token < items[j].Token
		}
		return items[i].ModTime > items[j].ModTime
	})

	return items, nil
}

func readFirstNonEmptyLine(path string, maxBytes int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	r := bufio.NewReader(f)
	limit := maxBytes
	if limit <= 0 {
		limit = 64 << 10
	}

	var total int
	for {
		line, err := r.ReadString('\n')
		total += len(line)
		if total > limit {
			return "", fmt.Errorf("line too long")
		}
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			return trimmed, nil
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return "", io.EOF
			}
			return "", err
		}
	}
}

func readLastNonEmptyLine(path string, maxBytes int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	if st.Size() <= 0 {
		return "", io.EOF
	}

	n := int64(maxBytes)
	if n <= 0 {
		n = 64 << 10
	}
	if n > st.Size() {
		n = st.Size()
	}

	if _, err := f.Seek(-n, io.SeekEnd); err != nil {
		return "", err
	}

	buf := make([]byte, n)
	if _, err := io.ReadFull(f, buf); err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", err
	}

	s := strings.TrimSpace(string(buf))
	if s == "" {
		return "", io.EOF
	}

	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed != "" {
			return trimmed, nil
		}
	}
	return "", io.EOF
}
