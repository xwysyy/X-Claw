package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMemoryStore_SearchRelevant_FTSIndexesAllMarkdown(t *testing.T) {
	workspace := t.TempDir()
	ms := NewMemoryStore(workspace)
	ms.SetVectorSettings(MemoryVectorSettings{
		Enabled:         false, // force FTS-only search
		Dimensions:      128,
		TopK:            6,
		MinScore:        0.01,
		MaxContextChars: 1200,
		RecentDailyDays: 1, // should not affect FTS coverage
	})

	memoryDir := filepath.Join(workspace, "memory")
	if err := os.MkdirAll(memoryDir, 0o755); err != nil {
		t.Fatalf("mkdir memory dir: %v", err)
	}

	content := `# MEMORY

## Long-term Facts
- Favorite animal: walrus
`
	if err := os.WriteFile(filepath.Join(memoryDir, "MEMORY.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write MEMORY.md: %v", err)
	}

	// Create an old daily note far outside RecentDailyDays.
	oldPath := filepath.Join(memoryDir, "199912", "19991231.md")
	if err := os.MkdirAll(filepath.Dir(oldPath), 0o755); err != nil {
		t.Fatalf("mkdir old note dir: %v", err)
	}
	old := "# 1999-12-31\n\n- Secret keyword: walrus\n"
	if err := os.WriteFile(oldPath, []byte(old), 0o644); err != nil {
		t.Fatalf("write old daily note: %v", err)
	}

	hits, err := ms.SearchRelevant(context.Background(), "walrus", 10, 0.01)
	if err != nil {
		t.Fatalf("SearchRelevant failed: %v", err)
	}
	if len(hits) == 0 {
		t.Fatalf("expected FTS hits, got 0")
	}

	foundOld := false
	for _, hit := range hits {
		if strings.Contains(hit.Source, "199912/19991231.md") && strings.Contains(strings.ToLower(hit.Text), "walrus") {
			foundOld = true
			break
		}
	}
	if !foundOld {
		t.Fatalf("expected hit from old daily note, got: %+v", hits)
	}

	indexPath := filepath.Join(memoryDir, "fts", "index.sqlite")
	if _, err := os.Stat(indexPath); err != nil {
		t.Fatalf("expected fts index at %s: %v", indexPath, err)
	}
}
