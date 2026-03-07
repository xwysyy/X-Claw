package tools

import (
	"errors"
	"strings"
	"testing"
)

func startTestProcessSession(pm *ProcessManager, command string) string {
	return pm.StartSession(command, "/tmp", nil, nil, nil)
}

func markTestProcessCompleted(pm *ProcessManager, sessionID string) {
	pm.MarkExited(sessionID, nil, false)
}

func markTestProcessFailed(pm *ProcessManager, sessionID string, err error) {
	pm.MarkExited(sessionID, err, false)
}

func markTestProcessKilled(pm *ProcessManager, sessionID string) {
	session, ok := pm.getSession(sessionID)
	if !ok {
		return
	}
	session.mu.Lock()
	session.killRequested = true
	session.mu.Unlock()
	pm.MarkExited(sessionID, nil, false)
}

func TestShellSessionTable_PrunesTerminalSessionsAtCapacity(t *testing.T) {
	pm := NewProcessManager(1024)
	pm.maxSessions = 3

	completedID := startTestProcessSession(pm, "echo completed")
	markTestProcessCompleted(pm, completedID)
	completed, ok := pm.GetSnapshot(completedID)
	if !ok || completed.Status != "completed" {
		t.Fatalf("expected completed session, got ok=%v status=%q", ok, completed.Status)
	}

	failedID := startTestProcessSession(pm, "echo failed")
	markTestProcessFailed(pm, failedID, errors.New("boom"))
	failed, ok := pm.GetSnapshot(failedID)
	if !ok || failed.Status != "failed" {
		t.Fatalf("expected failed session, got ok=%v status=%q", ok, failed.Status)
	}
	if !strings.Contains(failed.ExitError, "boom") {
		t.Fatalf("expected failed exit error to be preserved, got %q", failed.ExitError)
	}

	killedID := startTestProcessSession(pm, "sleep 60")
	markTestProcessKilled(pm, killedID)
	killed, ok := pm.GetSnapshot(killedID)
	if !ok || killed.Status != "killed" {
		t.Fatalf("expected killed session, got ok=%v status=%q", ok, killed.Status)
	}

	runningID := startTestProcessSession(pm, "cat")
	if _, ok := pm.GetSnapshot(completedID); ok {
		t.Fatalf("expected oldest terminal session %q to be pruned", completedID)
	}
	if _, ok := pm.GetSnapshot(failedID); !ok {
		t.Fatalf("expected failed session %q to remain after prune", failedID)
	}
	if _, ok := pm.GetSnapshot(killedID); !ok {
		t.Fatalf("expected killed session %q to remain after prune", killedID)
	}
	running, ok := pm.GetSnapshot(runningID)
	if !ok || running.Status != "running" {
		t.Fatalf("expected running session to remain, got ok=%v status=%q", ok, running.Status)
	}
	if got := len(pm.ListSnapshots()); got != pm.maxSessions {
		t.Fatalf("expected %d sessions after prune, got %d", pm.maxSessions, got)
	}
}

func TestBackgroundSession_ClearRemovesTerminalSessionsOnly(t *testing.T) {
	pm := NewProcessManager(1024)

	runningID := startTestProcessSession(pm, "cat")
	if err := pm.Clear(runningID); !errors.Is(err, ErrProcessSessionRunning) {
		t.Fatalf("expected ErrProcessSessionRunning, got %v", err)
	}

	failedID := startTestProcessSession(pm, "false")
	markTestProcessFailed(pm, failedID, errors.New("failed command"))
	if err := pm.Clear(failedID); err != nil {
		t.Fatalf("clear terminal session failed: %v", err)
	}
	if _, ok := pm.GetSnapshot(failedID); ok {
		t.Fatalf("expected cleared session %q to be removed", failedID)
	}
}

func TestBackgroundSession_RemoveSignalsRunningThenRemovesKilledSession(t *testing.T) {
	pm := NewProcessManager(1024)

	sessionID := startTestProcessSession(pm, "sleep 60")
	removed, err := pm.Remove(sessionID)
	if err != nil {
		t.Fatalf("remove running session returned error: %v", err)
	}
	if removed {
		t.Fatalf("expected running remove to report removed=false")
	}

	session, ok := pm.getSession(sessionID)
	if !ok {
		t.Fatalf("expected session %q to remain until exit", sessionID)
	}
	session.mu.Lock()
	killRequested := session.killRequested
	session.mu.Unlock()
	if !killRequested {
		t.Fatalf("expected remove to request kill for running session %q", sessionID)
	}

	pm.MarkExited(sessionID, nil, false)
	snapshot, ok := pm.GetSnapshot(sessionID)
	if !ok || snapshot.Status != "killed" {
		t.Fatalf("expected killed snapshot after exit, got ok=%v status=%q", ok, snapshot.Status)
	}

	removed, err = pm.Remove(sessionID)
	if err != nil {
		t.Fatalf("remove terminal session returned error: %v", err)
	}
	if !removed {
		t.Fatalf("expected terminal remove to report removed=true")
	}
	if _, ok := pm.GetSnapshot(sessionID); ok {
		t.Fatalf("expected removed session %q to be deleted", sessionID)
	}
}
