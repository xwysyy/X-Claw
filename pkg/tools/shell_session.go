package tools

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	defaultProcessMaxOutputChars  = 30000
	defaultProcessMaxPendingChars = 12000
	defaultProcessLogTailLines    = 200
	defaultProcessMaxSessions     = 128
)

var (
	ErrProcessSessionNotFound = errors.New("process session not found")
	ErrProcessSessionRunning  = errors.New("process session is still running")
)

type processSession struct {
	ID      string
	Command string
	CWD     string

	StartedAt time.Time
	UpdatedAt time.Time
	EndedAt   time.Time

	PID       int
	Status    string
	ExitCode  *int
	ExitError string
	Truncated bool

	output        string
	pending       string
	cmd           *exec.Cmd
	stdin         io.WriteCloser
	cancel        func()
	killRequested bool

	notify chan struct{}
	mu     sync.Mutex
}

type ProcessSessionSnapshot struct {
	SessionID string `json:"session_id"`
	Status    string `json:"status"`
	Command   string `json:"command"`
	CWD       string `json:"cwd,omitempty"`
	PID       int    `json:"pid,omitempty"`

	StartedAt string `json:"started_at"`
	UpdatedAt string `json:"updated_at"`
	EndedAt   string `json:"ended_at,omitempty"`

	ExitCode  *int   `json:"exit_code,omitempty"`
	ExitError string `json:"exit_error,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

type ProcessPollResult struct {
	Session  ProcessSessionSnapshot `json:"session"`
	Output   string                 `json:"output,omitempty"`
	TimedOut bool                   `json:"timed_out,omitempty"`
}

type ProcessLogResult struct {
	Session    ProcessSessionSnapshot `json:"session"`
	TotalLines int                    `json:"total_lines"`
	Offset     int                    `json:"offset"`
	Limit      int                    `json:"limit,omitempty"`
	Lines      []string               `json:"lines"`
	Output     string                 `json:"output"`
}

type ProcessManager struct {
	mu              sync.RWMutex
	sessions        map[string]*processSession
	nextID          atomic.Uint64
	maxOutputChars  int
	maxPendingChars int
	maxSessions     int
}

func NewProcessManager(maxOutputChars int) *ProcessManager {
	if maxOutputChars <= 0 {
		maxOutputChars = defaultProcessMaxOutputChars
	}

	maxPendingChars := defaultProcessMaxPendingChars
	if maxPendingChars > maxOutputChars {
		maxPendingChars = maxOutputChars
	}

	return &ProcessManager{
		sessions:        make(map[string]*processSession),
		maxOutputChars:  maxOutputChars,
		maxPendingChars: maxPendingChars,
		maxSessions:     defaultProcessMaxSessions,
	}
}

func (pm *ProcessManager) StartSession(
	command, cwd string,
	cmd *exec.Cmd,
	stdin io.WriteCloser,
	cancel func(),
) string {
	id := fmt.Sprintf("proc-%d", pm.nextID.Add(1))
	now := time.Now()

	pid := 0
	if cmd != nil && cmd.Process != nil {
		pid = cmd.Process.Pid
	}

	session := &processSession{
		ID:        id,
		Command:   command,
		CWD:       cwd,
		StartedAt: now,
		UpdatedAt: now,
		PID:       pid,
		Status:    "running",
		cmd:       cmd,
		stdin:     stdin,
		cancel:    cancel,
		notify:    make(chan struct{}, 1),
	}

	pm.mu.Lock()
	pm.sessions[id] = session
	pm.pruneTerminalSessionsLocked(id)
	pm.mu.Unlock()

	return id
}

func (pm *ProcessManager) AppendOutput(sessionID, chunk string) {
	if chunk == "" {
		return
	}

	session, ok := pm.getSession(sessionID)
	if !ok {
		return
	}

	session.mu.Lock()
	defer session.mu.Unlock()

	session.output, session.Truncated = appendWithCap(
		session.output,
		chunk,
		pm.maxOutputChars,
		session.Truncated,
	)
	session.pending, session.Truncated = appendWithCap(
		session.pending,
		chunk,
		pm.maxPendingChars,
		session.Truncated,
	)
	session.UpdatedAt = time.Now()
	session.signalNotifyLocked()
}

func (pm *ProcessManager) MarkExited(sessionID string, waitErr error, timedOut bool) {
	session, ok := pm.getSession(sessionID)
	if !ok {
		return
	}

	session.mu.Lock()
	now := time.Now()
	session.UpdatedAt = now
	session.EndedAt = now

	switch {
	case timedOut:
		session.Status = "timeout"
	case session.killRequested:
		session.Status = "killed"
	case waitErr != nil:
		session.Status = "failed"
	default:
		session.Status = "completed"
	}

	if waitErr != nil {
		session.ExitError = waitErr.Error()
	}
	if code, ok := extractExitCode(waitErr); ok {
		session.ExitCode = &code
	} else if waitErr == nil {
		code := 0
		session.ExitCode = &code
	}

	if session.stdin != nil {
		_ = session.stdin.Close()
		session.stdin = nil
	}
	if session.cancel != nil {
		session.cancel()
		session.cancel = nil
	}
	session.cmd = nil
	session.signalNotifyLocked()
	session.mu.Unlock()

	pm.mu.Lock()
	pm.pruneTerminalSessionsLocked(sessionID)
	pm.mu.Unlock()
}

func (pm *ProcessManager) GetSnapshot(sessionID string) (ProcessSessionSnapshot, bool) {
	session, ok := pm.getSession(sessionID)
	if !ok {
		return ProcessSessionSnapshot{}, false
	}

	session.mu.Lock()
	defer session.mu.Unlock()
	return session.snapshotLocked(), true
}

func (pm *ProcessManager) ListSnapshots() []ProcessSessionSnapshot {
	pm.mu.RLock()
	sessions := make([]*processSession, 0, len(pm.sessions))
	for _, session := range pm.sessions {
		sessions = append(sessions, session)
	}
	pm.mu.RUnlock()

	out := make([]ProcessSessionSnapshot, 0, len(sessions))
	for _, session := range sessions {
		session.mu.Lock()
		out = append(out, session.snapshotLocked())
		session.mu.Unlock()
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].StartedAt > out[j].StartedAt
	})
	return out
}

func (pm *ProcessManager) getSession(sessionID string) (*processSession, bool) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	session, ok := pm.sessions[sessionID]
	return session, ok
}

func (pm *ProcessManager) pruneTerminalSessionsLocked(preserve string) {
	if pm == nil || pm.maxSessions <= 0 || len(pm.sessions) <= pm.maxSessions {
		return
	}

	type candidate struct {
		id        string
		updatedAt time.Time
	}

	terminal := make([]candidate, 0, len(pm.sessions))
	for id, session := range pm.sessions {
		if id == preserve || session == nil {
			continue
		}
		session.mu.Lock()
		status := session.Status
		updatedAt := session.UpdatedAt
		session.mu.Unlock()
		if status == "running" {
			continue
		}
		terminal = append(terminal, candidate{id: id, updatedAt: updatedAt})
	}

	if len(terminal) == 0 {
		return
	}

	sort.Slice(terminal, func(i, j int) bool {
		if terminal[i].updatedAt.Equal(terminal[j].updatedAt) {
			return terminal[i].id < terminal[j].id
		}
		return terminal[i].updatedAt.Before(terminal[j].updatedAt)
	})

	for len(pm.sessions) > pm.maxSessions && len(terminal) > 0 {
		victim := terminal[0]
		terminal = terminal[1:]
		delete(pm.sessions, victim.id)
	}
}

func (s *processSession) snapshot() ProcessSessionSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

func (s *processSession) drainPending() (string, ProcessSessionSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()

	pending := s.pending
	s.pending = ""
	return pending, s.snapshotLocked()
}

func (s *processSession) outputSnapshot() (string, ProcessSessionSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.output, s.snapshotLocked()
}

func (s *processSession) snapshotLocked() ProcessSessionSnapshot {
	snapshot := ProcessSessionSnapshot{
		SessionID: s.ID,
		Status:    s.Status,
		Command:   s.Command,
		CWD:       s.CWD,
		PID:       s.PID,
		StartedAt: s.StartedAt.Format(time.RFC3339),
		UpdatedAt: s.UpdatedAt.Format(time.RFC3339),
		ExitCode:  s.ExitCode,
		ExitError: s.ExitError,
		Truncated: s.Truncated,
	}
	if !s.EndedAt.IsZero() {
		snapshot.EndedAt = s.EndedAt.Format(time.RFC3339)
	}
	return snapshot
}

func (s *processSession) signalNotifyLocked() {
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

func appendWithCap(current, appendChunk string, max int, truncated bool) (string, bool) {
	if max <= 0 {
		return current + appendChunk, truncated
	}

	combined := current + appendChunk
	if len(combined) <= max {
		return combined, truncated
	}

	return combined[len(combined)-max:], true
}

func normalizeOutputLines(output string) []string {
	if output == "" {
		return []string{}
	}
	normalized := strings.ReplaceAll(output, "\r\n", "\n")
	normalized = strings.TrimSuffix(normalized, "\n")
	if normalized == "" {
		return []string{}
	}
	return strings.Split(normalized, "\n")
}

func extractExitCode(err error) (int, bool) {
	if err == nil {
		return 0, false
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), true
	}
	return 0, false
}

func prepareCommandForTermination(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if runtime.GOOS == "windows" {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminateProcessTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}

	pid := cmd.Process.Pid
	if pid <= 0 {
		return nil
	}

	if runtime.GOOS == "windows" {
		_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run()
		_ = cmd.Process.Kill()
		return nil
	}

	ownPgrp := syscall.Getpgrp()
	if pid > 1 && pid != ownPgrp {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
	_ = cmd.Process.Kill()
	return nil
}
