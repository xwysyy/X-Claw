package auditlog

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xwysyy/X-Claw/pkg/config"
)

func configureTestAuditLog(t *testing.T, cfg config.AuditLogConfig) string {
	t.Helper()

	workspace := t.TempDir()
	if strings.TrimSpace(cfg.Dir) == "" {
		cfg.Dir = filepath.Join(workspace, "audit")
	}
	Configure(workspace, cfg)
	t.Cleanup(func() {
		writers.Delete(workspace)
	})
	return workspace
}

func readAuditLines(t *testing.T, path string) []string {
	t.Helper()

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open audit file: %v", err)
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		text := strings.TrimSpace(scanner.Text())
		if text != "" {
			lines = append(lines, text)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan audit file: %v", err)
	}
	return lines
}

func TestRecord_AppendsToFile(t *testing.T) {
	workspace := configureTestAuditLog(t, config.AuditLogConfig{Enabled: true})
	auditPath := filepath.Join(workspace, "audit", "audit.jsonl")

	Record(workspace, Event{Type: "test.record", Note: "hello"})

	lines := readAuditLines(t, auditPath)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}

	var got Event
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("unmarshal audit line: %v", err)
	}
	if got.Type != "test.record" {
		t.Fatalf("unexpected type %q", got.Type)
	}
	if got.Note != "hello" {
		t.Fatalf("unexpected note %q", got.Note)
	}
	if got.Workspace != workspace {
		t.Fatalf("unexpected workspace %q", got.Workspace)
	}
	if strings.TrimSpace(got.TS) == "" || got.TSMS == 0 {
		t.Fatalf("expected ts fields to be populated: %+v", got)
	}
}

func TestVerifyHMACSignature(t *testing.T) {
	key := []byte("super-secret")
	ev := Event{
		Type:      "test.verify",
		TS:        time.Now().UTC().Format(time.RFC3339Nano),
		TSMS:      time.Now().UnixMilli(),
		Workspace: "ws",
		Note:      "signed",
	}
	sig, err := computeHMACSignatureHex(ev, key)
	if err != nil {
		t.Fatalf("compute signature: %v", err)
	}
	ev.Sig = sig

	ok, err := VerifyHMACSignature(ev, key)
	if err != nil {
		t.Fatalf("verify signature: %v", err)
	}
	if !ok {
		t.Fatal("expected signature to verify")
	}

	tampered := ev
	tampered.Note = "tampered"
	ok, err = VerifyHMACSignature(tampered, key)
	if err != nil {
		t.Fatalf("verify tampered signature: %v", err)
	}
	if ok {
		t.Fatal("expected tampered signature to fail")
	}

	if _, err := VerifyHMACSignature(ev, nil); err == nil {
		t.Fatal("expected missing key to return error")
	}
}

func TestRotation(t *testing.T) {
	workspace := configureTestAuditLog(t, config.AuditLogConfig{
		Enabled:    true,
		MaxBytes:   200,
		MaxBackups: 1,
	})
	auditDir := filepath.Join(workspace, "audit")

	Record(workspace, Event{Type: "test.rotate", Note: strings.Repeat("a", 220)})
	Record(workspace, Event{Type: "test.rotate", Note: strings.Repeat("b", 220)})

	entries, err := os.ReadDir(auditDir)
	if err != nil {
		t.Fatalf("read audit dir: %v", err)
	}
	var rotated int
	for _, entry := range entries {
		if entry == nil || entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, "audit-") && strings.HasSuffix(name, ".jsonl") {
			rotated++
		}
	}
	if rotated == 0 {
		t.Fatalf("expected rotated audit file in %q", auditDir)
	}
}

func TestConcurrentWrites(t *testing.T) {
	workspace := configureTestAuditLog(t, config.AuditLogConfig{Enabled: true})
	auditPath := filepath.Join(workspace, "audit", "audit.jsonl")

	const total = 24
	var wg sync.WaitGroup
	for i := 0; i < total; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			Record(workspace, Event{Type: "test.concurrent", Note: "item"})
		}(i)
	}
	wg.Wait()

	lines := readAuditLines(t, auditPath)
	if len(lines) != total {
		t.Fatalf("expected %d lines, got %d", total, len(lines))
	}
}
