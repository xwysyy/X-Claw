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
	"strconv"
	"strings"
	"time"
)

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

	st, err := os.Stat(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			h.writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "file not found"})
			return
		}
		h.writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
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

	content, truncated, err := tailLines(abs, lines, 1<<20)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			h.writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "file not found"})
			return
		}
		h.writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
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

func (h *ConsoleHandler) handleStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "method not allowed"})
		return
	}

	rel := strings.TrimSpace(r.URL.Query().Get("path"))
	if rel == "" {
		h.writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "path is required"})
		return
	}

	tail := 200
	if v := strings.TrimSpace(r.URL.Query().Get("tail")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			tail = n
		}
	}
	if tail < 0 {
		tail = 0
	}
	if tail > 500 {
		tail = 500
	}

	abs, relClean, err := h.resolveConsolePath(rel)
	if err != nil {
		h.writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	f, err := os.Open(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			h.writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "file not found"})
			return
		}
		h.writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	defer f.Close()

	flusher, ok := w.(http.Flusher)
	if !ok {
		h.writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "streaming not supported"})
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	if tail > 0 {
		const maxBytes = int64(1 << 20)
		if st, err := f.Stat(); err == nil && st != nil && st.Size() > 0 {
			start := st.Size() - maxBytes
			if start < 0 {
				start = 0
			}
			if _, err := f.Seek(start, io.SeekStart); err == nil {
				buf, _ := io.ReadAll(f)
				lines := strings.Split(string(buf), "\n")
				if start > 0 && len(lines) > 0 {
					lines = lines[1:]
				}
				trimmed := make([]string, 0, len(lines))
				for _, line := range lines {
					line = strings.TrimSpace(line)
					if line == "" {
						continue
					}
					trimmed = append(trimmed, line)
				}
				if len(trimmed) > tail {
					trimmed = trimmed[len(trimmed)-tail:]
				}
				for _, line := range trimmed {
					_, _ = io.WriteString(w, line+"\n")
				}
				flusher.Flush()
			}
		}
	}

	reader := bufio.NewReader(f)
	lastKeepAlive := time.Now()
	lastStat := time.Now()
	var lastSize int64
	if st, err := f.Stat(); err == nil && st != nil {
		lastSize = st.Size()
	}

	for {
		select {
		case <-r.Context().Done():
			return
		default:
			line, err := reader.ReadString('\n')
			if err == nil {
				line = strings.TrimSpace(line)
				if line != "" {
					_, _ = io.WriteString(w, line+"\n")
					flusher.Flush()
				}
				continue
			}

			if !errors.Is(err, io.EOF) {
				_, _ = io.WriteString(w, fmt.Sprintf("{\"ok\":false,\"error\":%q,\"path\":%q}\n", err.Error(), relClean))
				flusher.Flush()
				return
			}

			if time.Since(lastKeepAlive) > 10*time.Second {
				_, _ = io.WriteString(w, "\n")
				flusher.Flush()
				lastKeepAlive = time.Now()
			}

			if time.Since(lastStat) > 2*time.Second {
				lastStat = time.Now()
				if st, err := os.Stat(abs); err == nil && st != nil {
					if st.Size() < lastSize {
						if _, err := f.Seek(0, io.SeekStart); err == nil {
							reader.Reset(f)
						}
					}
					lastSize = st.Size()
				}
			}

			time.Sleep(150 * time.Millisecond)
		}
	}
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

func (h *ConsoleHandler) resolveConsolePath(raw string) (abs string, relClean string, err error) {
	if h == nil || strings.TrimSpace(h.workspace) == "" {
		return "", "", fmt.Errorf("workspace not configured")
	}

	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "/")
	raw = filepath.Clean(filepath.FromSlash(raw))

	if raw == "." || raw == "" {
		return "", "", fmt.Errorf("invalid path")
	}
	if !filepath.IsLocal(raw) {
		return "", "", fmt.Errorf("invalid path")
	}

	allowedPrefixes := []string{
		filepath.Join(".x-claw", "audit"),
		"cron",
		"state",
		"sessions",
	}
	allowed := false
	for _, p := range allowedPrefixes {
		if raw == p || strings.HasPrefix(raw, p+string(os.PathSeparator)) {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", "", fmt.Errorf("path not allowed")
	}

	ext := strings.ToLower(filepath.Ext(raw))
	switch ext {
	case ".json", ".jsonl", ".md", ".txt", ".log":
	default:
		return "", "", fmt.Errorf("file type not allowed")
	}

	absPath := filepath.Join(h.workspace, raw)
	baseResolved := strings.TrimSpace(h.workspaceResolved)
	if baseResolved == "" {
		if rs, err := filepath.EvalSymlinks(h.workspace); err == nil {
			baseResolved = rs
		} else {
			baseResolved = h.workspace
		}
	}
	if rs, err := filepath.EvalSymlinks(absPath); err == nil {
		basePrefix := baseResolved + string(os.PathSeparator)
		if rs != baseResolved && !strings.HasPrefix(rs, basePrefix) {
			return "", "", fmt.Errorf("path escapes workspace")
		}
	}

	return absPath, filepath.ToSlash(raw), nil
}

func tailLines(path string, maxLines int, maxBytes int64) ([]string, bool, error) {
	if maxLines <= 0 {
		return []string{}, false, nil
	}
	if maxBytes <= 0 {
		maxBytes = 1 << 20
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	if st.Size() <= 0 {
		return []string{}, false, nil
	}

	n := maxBytes
	if n > st.Size() {
		n = st.Size()
	}

	if _, err := f.Seek(-n, io.SeekEnd); err != nil {
		return nil, false, err
	}

	buf := make([]byte, n)
	readN, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, false, err
	}
	buf = buf[:readN]

	lines := strings.Split(string(buf), "\n")
	truncated := st.Size() > n
	if truncated && len(lines) > 0 {
		lines = lines[1:]
	}

	out := make([]string, 0, maxLines)
	for i := len(lines) - 1; i >= 0 && len(out) < maxLines; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, truncated, nil
}
