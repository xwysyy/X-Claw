package httpapi

import (
	"fmt"
	"io"
	"net/http"

	"github.com/xwysyy/X-Claw/pkg/logger"
)

func (h *ConsoleHandler) writeConsoleInternalError(w http.ResponseWriter, publicMessage, logMessage string, fields map[string]any, err error) {
	if fields == nil {
		fields = map[string]any{}
	}
	if err != nil {
		fields["error"] = err.Error()
	}
	logger.WarnCF("httpapi", logMessage, fields)
	h.writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": publicMessage})
}

func writeConsoleStreamInternalError(w io.Writer, flusher http.Flusher, relPath, publicMessage string) {
	_, _ = io.WriteString(w, fmt.Sprintf("{\"ok\":false,\"error\":%q,\"path\":%q}\n", publicMessage, relPath))
	flusher.Flush()
}

func logConsoleStreamFailure(logMessage string, fields map[string]any, err error) {
	if fields == nil {
		fields = map[string]any{}
	}
	if err != nil {
		fields["error"] = err.Error()
	}
	logger.WarnCF("httpapi", logMessage, fields)
}
