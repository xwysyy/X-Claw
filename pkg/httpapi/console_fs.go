package httpapi

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

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
