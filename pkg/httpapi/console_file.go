package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/xwysyy/X-Claw/pkg/logger"
)

var (
	consoleFileStat  = os.Stat
	consoleTailLines = tailLines
)

func logConsoleFileError(op, path string, err error) {
	if err == nil {
		return
	}
	logger.WarnCF("httpapi", "Console file operation failed", map[string]any{
		"operation": op,
		"path":      path,
		"error":     err.Error(),
	})
}

func (h *ConsoleHandler) handleFile(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimSpace(r.URL.Query().Get("path"))
	if rel == "" {
		h.writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "path is required"})
		return
	}

	abs, relClean, err := h.resolveConsolePath(rel)
	if err != nil {
		h.writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	st, err := consoleFileStat(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			h.writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "file not found"})
			return
		}
		h.writeConsoleInternalError(w, "failed to access file", "Console file stat failed", map[string]any{
			"path": relClean,
		}, err)
		return
	}
	if st.IsDir() {
		h.writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "path must be a file"})
		return
	}

	name := filepath.Base(relClean)
	w.Header().Set("Content-Type", contentTypeForExt(filepath.Ext(name)))
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	http.ServeFile(w, r, abs)
}

func (h *ConsoleHandler) handleTail(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimSpace(r.URL.Query().Get("path"))
	if rel == "" {
		h.writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "path is required"})
		return
	}

	lines := 200
	if v := strings.TrimSpace(r.URL.Query().Get("lines")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			lines = n
		}
	}
	if lines <= 0 {
		lines = 200
	}
	if lines > 500 {
		lines = 500
	}

	abs, relClean, err := h.resolveConsolePath(rel)
	if err != nil {
		h.writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	content, truncated, err := consoleTailLines(abs, lines, 1<<20)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			h.writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "file not found"})
			return
		}
		h.writeConsoleInternalError(w, "failed to read file", "Console tail read failed", map[string]any{
			"path":  relClean,
			"lines": lines,
		}, err)
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]any{
		"ok":              true,
		"path":            relClean,
		"lines_requested": lines,
		"truncated":       truncated,
		"lines":           content,
	})
}

func contentTypeForExt(ext string) string {
	switch strings.ToLower(strings.TrimSpace(ext)) {
	case ".json":
		return "application/json"
	case ".jsonl":
		return "text/plain; charset=utf-8"
	case ".md", ".txt", ".log":
		return "text/plain; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}
