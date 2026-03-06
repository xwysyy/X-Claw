package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xwysyy/X-Claw/pkg/cron"
)

func (h *ConsoleHandler) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (h *ConsoleHandler) loadState() (any, error) {
	statePath := filepath.Join(h.workspace, "state", "state.json")
	data, err := os.ReadFile(statePath)
	if err != nil {
		return nil, err
	}
	var obj any
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil, fmt.Errorf("invalid state json")
	}
	return obj, nil
}

func (h *ConsoleHandler) summarizeCron() map[string]any {
	storePath := filepath.Join(h.workspace, "cron", "jobs.json")
	st, err := os.Stat(storePath)
	if err != nil {
		return map[string]any{
			"path":    filepath.ToSlash(filepath.Join("cron", "jobs.json")),
			"exists":  false,
			"jobs":    0,
			"modTime": "",
		}
	}

	jobsCount := 0
	if data, err := os.ReadFile(storePath); err == nil {
		var store cron.CronStore
		if json.Unmarshal(data, &store) == nil {
			jobsCount = len(store.Jobs)
		}
	}

	return map[string]any{
		"path":    filepath.ToSlash(filepath.Join("cron", "jobs.json")),
		"exists":  true,
		"jobs":    jobsCount,
		"modTime": st.ModTime().UTC().Format(time.RFC3339Nano),
		"size":    st.Size(),
	}
}

func (h *ConsoleHandler) countTraceSessions(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, ent.Name(), "events.jsonl")); err == nil {
			n++
		}
	}
	return n
}

func pickConsoleStaticDir() string {
	candidates := []string{}
	if home, _ := os.UserHomeDir(); strings.TrimSpace(home) != "" {
		candidates = append(candidates,
			filepath.Join(home, ".x-claw", "console"),
			filepath.Join(home, ".local", "share", "x-claw", "console"),
		)
	}
	candidates = append(candidates,
		"/usr/local/share/x-claw/console",
		filepath.Join("web", "x-claw-console", "out"),
	)
	for _, p := range candidates {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			return p
		}
	}
	return ""
}
