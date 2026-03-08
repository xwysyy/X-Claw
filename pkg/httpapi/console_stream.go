package httpapi

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/xwysyy/X-Claw/pkg/logger"
)

var (
	consoleStreamKeepAliveInterval = 10 * time.Second
	consoleStreamTailLines         = tailLines
	consoleStreamStatInterval      = 2 * time.Second
	consoleStreamIdleSleep         = 150 * time.Millisecond
	consoleStreamOpenFile          = os.Open
)

func reopenConsoleStreamFile(current *os.File, abs string) (*os.File, *bufio.Reader, os.FileInfo, error) {
	if current != nil {
		_ = current.Close()
	}
	reopened, err := os.Open(abs)
	if err != nil {
		return nil, nil, nil, err
	}
	info, err := reopened.Stat()
	if err != nil {
		_ = reopened.Close()
		return nil, nil, nil, err
	}
	return reopened, bufio.NewReader(reopened), info, nil
}

func writeConsoleStreamError(w http.ResponseWriter, flusher http.Flusher, relClean string, err error) {
	if err != nil {
		logger.WarnCF("httpapi", "Console stream failed", map[string]any{
			"path":  relClean,
			"error": err.Error(),
		})
	}
	_, _ = io.WriteString(w, fmt.Sprintf("{\"ok\":false,\"error\":%q,\"path\":%q}\n", "stream unavailable", relClean))
	flusher.Flush()
}

func (h *ConsoleHandler) handleStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
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

	f, err := consoleStreamOpenFile(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			h.writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "file not found"})
			return
		}
		logger.WarnCF("httpapi", "Console stream open failed", map[string]any{"path": relClean, "error": err.Error()})
		h.writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "stream unavailable"})
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
		lines, _, err := consoleStreamTailLines(abs, tail, maxBytes)
		if err != nil {
			writeConsoleStreamError(w, flusher, relClean, err)
			return
		}
		for _, line := range lines {
			_, _ = io.WriteString(w, line+"\n")
		}
		if _, err := f.Seek(0, io.SeekEnd); err != nil {
			writeConsoleStreamError(w, flusher, relClean, err)
			return
		}
		flusher.Flush()
	}

	reader := bufio.NewReader(f)
	openedInfo, _ := f.Stat()
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
				writeConsoleStreamError(w, flusher, relClean, err)
				return
			}

			if time.Since(lastKeepAlive) > consoleStreamKeepAliveInterval {
				_, _ = io.WriteString(w, "\n")
				flusher.Flush()
				lastKeepAlive = time.Now()
			}

			if time.Since(lastStat) > consoleStreamStatInterval {
				lastStat = time.Now()
				if st, err := os.Stat(abs); err == nil && st != nil {
					if openedInfo != nil && !os.SameFile(openedInfo, st) {
						reopened, reopenedReader, reopenedInfo, reopenErr := reopenConsoleStreamFile(f, abs)
						if reopenErr != nil {
							writeConsoleStreamError(w, flusher, relClean, reopenErr)
							return
						}
						f = reopened
						reader = reopenedReader
						openedInfo = reopenedInfo
						lastSize = 0
						continue
					}
					if st.Size() < lastSize {
						if _, err := f.Seek(0, io.SeekStart); err == nil {
							reader.Reset(f)
						}
					}
					openedInfo = st
					lastSize = st.Size()
				}
			}

			time.Sleep(consoleStreamIdleSleep)
		}
	}
}
