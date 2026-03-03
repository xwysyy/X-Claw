package auditlog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
)

type Event struct {
	Type string `json:"type"`

	TS   string `json:"ts"`
	TSMS int64  `json:"ts_ms"`

	Workspace string `json:"workspace,omitempty"`
	Source    string `json:"source,omitempty"`

	RunID      string `json:"run_id,omitempty"`
	SessionKey string `json:"session_key,omitempty"`
	Channel    string `json:"channel,omitempty"`
	ChatID     string `json:"chat_id,omitempty"`
	SenderID   string `json:"sender_id,omitempty"`

	Iteration int `json:"iteration,omitempty"`

	Tool       string `json:"tool,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`

	PolicyDecision  string `json:"policy_decision,omitempty"`
	PolicyReason    string `json:"policy_reason,omitempty"`
	PolicyTimeoutMS int    `json:"policy_timeout_ms,omitempty"`
	IdempotencyKey  string `json:"idempotency_key,omitempty"`

	DurationMS    int64  `json:"duration_ms,omitempty"`
	IsError       bool   `json:"is_error,omitempty"`
	Error         string `json:"error,omitempty"`
	ArgsPreview   string `json:"args_preview,omitempty"`
	ResultPreview string `json:"result_preview,omitempty"`

	Note string `json:"note,omitempty"`
}

type writer struct {
	workspace string

	enabled    bool
	dir        string
	maxBytes   int64
	maxBackups int

	path string

	mu sync.Mutex
}

var writers sync.Map // workspace -> *writer

func Configure(workspace string, cfg config.AuditLogConfig) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return
	}

	w := getOrCreateWriter(workspace)
	w.mu.Lock()
	defer w.mu.Unlock()

	w.enabled = cfg.Enabled
	w.dir = strings.TrimSpace(cfg.Dir)
	if w.dir == "" {
		w.dir = filepath.Join(workspace, ".picoclaw", "audit")
	}
	w.maxBytes = int64(cfg.MaxBytes)
	w.maxBackups = cfg.MaxBackups
	w.path = filepath.Join(w.dir, "audit.jsonl")
}

func Record(workspace string, ev Event) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return
	}

	w := getOrCreateWriter(workspace)
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.enabled {
		return
	}

	now := time.Now()
	if strings.TrimSpace(ev.TS) == "" {
		ev.TS = now.UTC().Format(time.RFC3339Nano)
	}
	if ev.TSMS == 0 {
		ev.TSMS = now.UnixMilli()
	}
	if strings.TrimSpace(ev.Workspace) == "" {
		ev.Workspace = workspace
	}

	if err := os.MkdirAll(w.dir, 0o755); err != nil {
		return
	}

	line, err := json.Marshal(ev)
	if err != nil {
		return
	}
	line = append(line, '\n')

	if w.maxBytes > 0 {
		if st, err := os.Stat(w.path); err == nil && st != nil {
			if st.Size()+int64(len(line)) > w.maxBytes {
				_ = w.rotateLocked(now)
			}
		}
	}

	f, err := os.OpenFile(w.path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	_, _ = f.Write(line)
	_ = f.Close()
}

func getOrCreateWriter(workspace string) *writer {
	if existing, ok := writers.Load(workspace); ok {
		if w, ok := existing.(*writer); ok && w != nil {
			return w
		}
	}

	w := &writer{
		workspace: workspace,
		enabled:   false,
		dir:       filepath.Join(workspace, ".picoclaw", "audit"),
		path:      filepath.Join(workspace, ".picoclaw", "audit", "audit.jsonl"),
	}
	actual, _ := writers.LoadOrStore(workspace, w)
	if ww, ok := actual.(*writer); ok && ww != nil {
		return ww
	}
	return w
}

func (w *writer) rotateLocked(now time.Time) error {
	if w == nil || strings.TrimSpace(w.path) == "" {
		return nil
	}

	if _, err := os.Stat(w.path); err != nil {
		return nil
	}

	base := strings.TrimSuffix(filepath.Base(w.path), filepath.Ext(w.path))
	rotName := fmt.Sprintf("%s-%s.jsonl", base, now.UTC().Format("20060102-150405"))
	rotPath := filepath.Join(w.dir, rotName)

	if err := os.Rename(w.path, rotPath); err != nil {
		return err
	}

	if w.maxBackups > 0 {
		w.pruneBackupsLocked(base)
	}
	return nil
}

func (w *writer) pruneBackupsLocked(base string) {
	entries, err := os.ReadDir(w.dir)
	if err != nil {
		return
	}

	type item struct {
		name string
		mod  time.Time
	}
	items := make([]item, 0, len(entries))
	prefix := base + "-"
	for _, e := range entries {
		if e == nil || e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil || info == nil {
			continue
		}
		items = append(items, item{name: name, mod: info.ModTime()})
	}
	if len(items) <= w.maxBackups {
		return
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].mod.Equal(items[j].mod) {
			return items[i].name < items[j].name
		}
		return items[i].mod.Before(items[j].mod)
	})

	toDelete := len(items) - w.maxBackups
	for i := 0; i < toDelete; i++ {
		_ = os.Remove(filepath.Join(w.dir, items[i].name))
	}
}
