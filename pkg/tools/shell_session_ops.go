package tools

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/xwysyy/X-Claw/pkg/logger"
)

func (pm *ProcessManager) Poll(sessionID string, timeout time.Duration) (ProcessPollResult, error) {
	session, ok := pm.getSession(sessionID)
	if !ok {
		return ProcessPollResult{}, ErrProcessSessionNotFound
	}
	if timeout < 0 {
		timeout = 0
	}

	deadline := time.Now().Add(timeout)
	for {
		output, snapshot := session.drainPending()
		if output != "" || snapshot.Status != "running" || timeout == 0 {
			return ProcessPollResult{
				Session: snapshot,
				Output:  output,
			}, nil
		}

		waitFor := time.Until(deadline)
		if waitFor <= 0 {
			return ProcessPollResult{
				Session:  snapshot,
				TimedOut: true,
			}, nil
		}

		select {
		case <-session.notify:
			// loop and re-check
		case <-time.After(waitFor):
			snapshot := session.snapshot()
			return ProcessPollResult{
				Session:  snapshot,
				TimedOut: true,
			}, nil
		}
	}
}

func (pm *ProcessManager) Log(
	sessionID string,
	offset, limit int,
	useDefaultTail bool,
) (ProcessLogResult, error) {
	session, ok := pm.getSession(sessionID)
	if !ok {
		return ProcessLogResult{}, ErrProcessSessionNotFound
	}

	output, snapshot := session.outputSnapshot()
	lines := normalizeOutputLines(output)
	total := len(lines)

	start := offset
	if start < 0 {
		start = 0
	}
	if start > total {
		start = total
	}

	end := total
	effectiveLimit := limit
	if useDefaultTail {
		if total > defaultProcessLogTailLines {
			start = total - defaultProcessLogTailLines
		} else {
			start = 0
		}
		end = total
		effectiveLimit = defaultProcessLogTailLines
	} else if effectiveLimit > 0 {
		if start+effectiveLimit < end {
			end = start + effectiveLimit
		}
	}

	window := []string{}
	if start < end {
		window = lines[start:end]
	}

	return ProcessLogResult{
		Session:    snapshot,
		TotalLines: total,
		Offset:     start,
		Limit:      effectiveLimit,
		Lines:      window,
		Output:     strings.Join(window, "\n"),
	}, nil
}

func (pm *ProcessManager) Write(sessionID, data string, eof bool) error {
	session, ok := pm.getSession(sessionID)
	if !ok {
		return ErrProcessSessionNotFound
	}

	session.mu.Lock()
	if session.Status != "running" {
		session.mu.Unlock()
		return fmt.Errorf("session %s is not running", sessionID)
	}
	stdin := session.stdin
	session.mu.Unlock()

	if stdin == nil {
		return fmt.Errorf("session %s has no writable stdin", sessionID)
	}

	if data != "" {
		if _, err := io.WriteString(stdin, data); err != nil {
			return err
		}
	}
	if eof {
		return stdin.Close()
	}
	return nil
}

func (pm *ProcessManager) Kill(sessionID string) (bool, error) {
	session, ok := pm.getSession(sessionID)
	if !ok {
		return false, ErrProcessSessionNotFound
	}

	session.mu.Lock()
	if session.Status != "running" {
		session.mu.Unlock()
		return false, nil
	}
	session.killRequested = true
	cmd := session.cmd
	cancel := session.cancel
	session.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if cmd != nil {
		if err := terminateProcessTree(cmd); err != nil {
			logger.DebugCF("tools/shell", "terminate process tree failed", map[string]any{"error": err.Error()})
		}
	}
	return true, nil
}

func (pm *ProcessManager) Clear(sessionID string) error {
	session, ok := pm.getSession(sessionID)
	if !ok {
		return ErrProcessSessionNotFound
	}

	session.mu.Lock()
	running := session.Status == "running"
	session.mu.Unlock()
	if running {
		return ErrProcessSessionRunning
	}

	pm.mu.Lock()
	delete(pm.sessions, sessionID)
	pm.mu.Unlock()
	return nil
}

func (pm *ProcessManager) Remove(sessionID string) (bool, error) {
	session, ok := pm.getSession(sessionID)
	if !ok {
		return false, ErrProcessSessionNotFound
	}

	session.mu.Lock()
	running := session.Status == "running"
	session.mu.Unlock()
	if running {
		_, err := pm.Kill(sessionID)
		return false, err
	}

	pm.mu.Lock()
	delete(pm.sessions, sessionID)
	pm.mu.Unlock()
	return true, nil
}
