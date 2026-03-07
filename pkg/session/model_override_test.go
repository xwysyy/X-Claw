package session

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestModelOverrideConcurrentAccess(t *testing.T) {
	sm := NewSessionManager("")
	key := "test-concurrent"

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			<-start
			model := "model-" + time.UnixMilli(int64(n+1)).UTC().Format("150405.000")
			if _, err := sm.SetModelOverride(key, model, time.Minute); err != nil {
				t.Errorf("SetModelOverride returned error: %v", err)
				return
			}
			if got, ok := sm.EffectiveModelOverride(key); ok && got == "" {
				t.Errorf("EffectiveModelOverride returned empty model with ok=true")
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if got, ok := sm.EffectiveModelOverride(key); !ok || got == "" {
		t.Fatalf("expected non-empty model override after concurrent access, got %q ok=%v", got, ok)
	}
}

func TestEffectiveModelOverrideClearsExpiredOverride(t *testing.T) {
	tmpDir := t.TempDir()
	sm := NewSessionManager(tmpDir)
	key := "test-expiry"

	expiresAt, err := sm.SetModelOverride(key, "fast-model", 15*time.Millisecond)
	if err != nil {
		t.Fatalf("SetModelOverride returned error: %v", err)
	}
	if expiresAt == nil {
		t.Fatalf("expected expiry timestamp to be set")
	}

	if got, ok := sm.EffectiveModelOverride(key); !ok || got != "fast-model" {
		t.Fatalf("expected fast-model before expiry, got %q ok=%v", got, ok)
	}

	time.Sleep(40 * time.Millisecond)

	if got, ok := sm.EffectiveModelOverride(key); ok || got != "" {
		t.Fatalf("expected expired override to be cleared, got %q ok=%v", got, ok)
	}

	snapshot, ok := sm.GetSessionSnapshot(key)
	if !ok {
		t.Fatalf("expected session snapshot for %q", key)
	}
	if snapshot.ModelOverride != "" {
		t.Fatalf("expected in-memory override cleared, got %q", snapshot.ModelOverride)
	}
	if snapshot.ModelOverrideExpiresAtMS != nil {
		t.Fatalf("expected expiry metadata cleared, got %v", *snapshot.ModelOverrideExpiresAtMS)
	}
}

func TestClearModelOverrideIsIdempotent(t *testing.T) {
	sm := NewSessionManager("")
	key := "test-clear"

	if _, err := sm.ClearModelOverride(key); err != nil {
		t.Fatalf("expected clearing absent override to succeed, got %v", err)
	}

	if _, err := sm.SetModelOverride(key, "some-model", 0); err != nil {
		t.Fatalf("SetModelOverride returned error: %v", err)
	}
	if _, err := sm.ClearModelOverride(key); err != nil {
		t.Fatalf("expected first clear to succeed, got %v", err)
	}
	if _, err := sm.ClearModelOverride(key); err != nil {
		t.Fatalf("expected second clear to succeed, got %v", err)
	}

	if got, ok := sm.EffectiveModelOverride(key); ok || got != "" {
		t.Fatalf("expected cleared override to stay empty, got %q ok=%v", got, ok)
	}
}

func TestEffectiveModelOverrideDoesNotClearFreshOverride(t *testing.T) {
	sm := NewSessionManager("")
	key := "test-refresh-after-expiry"

	for i := 0; i < 40; i++ {
		if _, err := sm.SetModelOverride(key, "expired", time.Millisecond); err != nil {
			t.Fatalf("SetModelOverride expired returned error: %v", err)
		}
		time.Sleep(2 * time.Millisecond)

		freshModel := fmt.Sprintf("fresh-%d", i)
		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			_, _ = sm.EffectiveModelOverride(key)
		}()

		go func(model string) {
			defer wg.Done()
			if _, err := sm.SetModelOverride(key, model, time.Minute); err != nil {
				t.Errorf("SetModelOverride fresh returned error: %v", err)
			}
		}(freshModel)

		wg.Wait()

		if got, ok := sm.EffectiveModelOverride(key); !ok || got != freshModel {
			t.Fatalf("iteration %d: expected fresh override %q to survive, got %q ok=%v", i, freshModel, got, ok)
		}
	}
}
