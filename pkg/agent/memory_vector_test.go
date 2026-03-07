package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMemoryStore_SearchRelevant_BuildsVectorIndex(t *testing.T) {
	workspace := t.TempDir()
	ms := NewMemoryStore(workspace)
	ms.SetVectorSettings(MemoryVectorSettings{
		Enabled:         true,
		Dimensions:      128,
		TopK:            4,
		MinScore:        0.01,
		MaxContextChars: 1200,
		RecentDailyDays: 7,
	})

	memoryDir := filepath.Join(workspace, "memory")
	if err := os.MkdirAll(memoryDir, 0o755); err != nil {
		t.Fatalf("mkdir memory dir: %v", err)
	}

	memoryContent := `# MEMORY

## Open Threads
- Renew passport in March
- Compare flight options for Tokyo trip
`
	if err := os.WriteFile(filepath.Join(memoryDir, "MEMORY.md"), []byte(memoryContent), 0o644); err != nil {
		t.Fatalf("write MEMORY.md: %v", err)
	}

	day := time.Now().Format("20060102")
	dayPath := filepath.Join(memoryDir, day[:6], day+".md")
	if err := os.MkdirAll(filepath.Dir(dayPath), 0o755); err != nil {
		t.Fatalf("mkdir daily dir: %v", err)
	}
	daily := "# Daily\n\n- Follow up with passport office tomorrow\n"
	if err := os.WriteFile(dayPath, []byte(daily), 0o644); err != nil {
		t.Fatalf("write daily note: %v", err)
	}

	hits, err := ms.SearchRelevant(context.Background(), "passport renewal", 3, 0.01)
	if err != nil {
		t.Fatalf("SearchRelevant failed: %v", err)
	}
	if len(hits) == 0 {
		t.Fatalf("expected semantic hits, got none")
	}

	joined := strings.ToLower(hits[0].Text)
	if !strings.Contains(joined, "passport") {
		t.Fatalf("expected top hit to mention passport, got %q", hits[0].Text)
	}

	indexPath := filepath.Join(memoryDir, "vector", "index.json")
	if _, err := os.Stat(indexPath); err != nil {
		t.Fatalf("expected vector index at %s: %v", indexPath, err)
	}
}

func TestMemoryStore_GetBySource(t *testing.T) {
	workspace := t.TempDir()
	ms := NewMemoryStore(workspace)
	ms.SetVectorSettings(MemoryVectorSettings{
		Enabled:         true,
		Dimensions:      128,
		TopK:            4,
		MinScore:        0.01,
		MaxContextChars: 1200,
		RecentDailyDays: 7,
	})

	memoryDir := filepath.Join(workspace, "memory")
	if err := os.MkdirAll(memoryDir, 0o755); err != nil {
		t.Fatalf("mkdir memory dir: %v", err)
	}

	memoryContent := `# MEMORY

## Open Threads
- Prepare tax documents by end of month
`
	if err := os.WriteFile(filepath.Join(memoryDir, "MEMORY.md"), []byte(memoryContent), 0o644); err != nil {
		t.Fatalf("write MEMORY.md: %v", err)
	}

	if _, err := ms.SearchRelevant(context.Background(), "tax documents", 3, 0.01); err != nil {
		t.Fatalf("SearchRelevant failed: %v", err)
	}

	hit, found, err := ms.GetBySource(context.Background(), "MEMORY.md#Open Threads")
	if err != nil {
		t.Fatalf("GetBySource failed: %v", err)
	}
	if !found {
		t.Fatal("expected source to be found")
	}
	if !strings.Contains(strings.ToLower(hit.Text), "tax") {
		t.Fatalf("expected hit text to include tax info, got: %q", hit.Text)
	}
}

func TestNormalizeMemoryVectorEmbeddingSettings_DefaultsToHashed(t *testing.T) {
	got := normalizeMemoryVectorEmbeddingSettings(MemoryVectorEmbeddingSettings{})
	if got.Kind != "hashed" {
		t.Fatalf("Kind = %q, want %q", got.Kind, "hashed")
	}
}

func TestNormalizeMemoryVectorEmbeddingSettings_DoesNotAutoSelectOpenAICompat(t *testing.T) {
	got := normalizeMemoryVectorEmbeddingSettings(MemoryVectorEmbeddingSettings{
		APIBase: "https://api.example.com/v1",
		Model:   "Qwen/Qwen3-Embedding-8B",
		APIKey:  "sk-test",
	})
	if got.Kind != "hashed" {
		t.Fatalf("Kind = %q, want %q", got.Kind, "hashed")
	}
}

func TestNormalizeMemoryVectorEmbeddingSettings_OpenAICompatIsNormalized(t *testing.T) {
	got := normalizeMemoryVectorEmbeddingSettings(MemoryVectorEmbeddingSettings{
		Kind:    "  OpenAI_Compat ",
		APIBase: "https://api.example.com/v1/",
		Model:   " text-embedding-3-large ",
	})
	if got.Kind != "openai_compat" {
		t.Fatalf("Kind = %q, want %q", got.Kind, "openai_compat")
	}
	if got.APIBase != "https://api.example.com/v1" {
		t.Fatalf("APIBase = %q, want %q", got.APIBase, "https://api.example.com/v1")
	}
	if got.Model != "text-embedding-3-large" {
		t.Fatalf("Model = %q, want %q", got.Model, "text-embedding-3-large")
	}
}

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

func TestMemoryOrganizeWriteback_DedupesAndSections(t *testing.T) {
	workspace := t.TempDir()
	ms := NewMemoryStore(workspace)

	initial := `# MEMORY

## Long-term Facts
- Likes tea
- Likes tea

## Open Threads
- Renew passport
`
	if err := os.WriteFile(filepath.Join(workspace, "memory", "MEMORY.md"), []byte(initial), 0o644); err != nil {
		t.Fatalf("write initial memory: %v", err)
	}

	update := `## Long-term Facts
- Likes tea
- Works remotely

## Active Goals
- Finish migration
`
	if err := ms.OrganizeWriteback(update); err != nil {
		t.Fatalf("OrganizeWriteback: %v", err)
	}

	got := ms.ReadLongTerm()
	if strings.Count(got, "- Likes tea") != 1 {
		t.Fatalf("expected deduped fact, got:\n%s", got)
	}
	if !strings.Contains(got, "## projects") {
		t.Fatalf("expected projects block, got:\n%s", got)
	}
	if !strings.Contains(got, "- Finish migration") {
		t.Fatalf("expected merged goal, got:\n%s", got)
	}
}

func TestMemorySearchTool_Execute(t *testing.T) {
	workspace := t.TempDir()
	ms := NewMemoryStore(workspace)
	ms.SetVectorSettings(MemoryVectorSettings{
		Enabled:         true,
		Dimensions:      128,
		TopK:            5,
		MinScore:        0.01,
		MaxContextChars: 1200,
		RecentDailyDays: 7,
	})

	if err := os.MkdirAll(filepath.Join(workspace, "memory"), 0o755); err != nil {
		t.Fatalf("mkdir memory dir: %v", err)
	}
	content := `# MEMORY

## Long-term Facts
- Favorite editor: neovim
`
	if err := os.WriteFile(filepath.Join(workspace, "memory", "MEMORY.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write memory: %v", err)
	}

	tool := NewMemorySearchTool(ms, 3, 0.01)
	result := tool.Execute(context.Background(), map[string]any{
		"query": "what editor do I use",
	})

	if result.IsError {
		t.Fatalf("expected successful tool result, got error: %s", result.ForLLM)
	}
	if !result.Silent {
		t.Fatalf("expected memory_search to be silent")
	}

	var parsed struct {
		Kind string `json:"kind"`
		Hits []struct {
			ID         string  `json:"id"`
			Score      float64 `json:"score"`
			Snippet    string  `json:"snippet"`
			Source     string  `json:"source"`
			SourcePath string  `json:"source_path"`
		} `json:"hits"`
	}
	if err := json.Unmarshal([]byte(result.ForLLM), &parsed); err != nil {
		t.Fatalf("expected JSON tool output, got unmarshal error: %v\nraw=%s", err, result.ForLLM)
	}
	if parsed.Kind != "memory_search_result" {
		t.Fatalf("kind = %q, want %q", parsed.Kind, "memory_search_result")
	}
	if len(parsed.Hits) == 0 {
		t.Fatalf("expected at least 1 hit, got 0")
	}
	if !strings.Contains(strings.ToLower(parsed.Hits[0].Snippet), "neovim") {
		t.Fatalf("expected first hit snippet to include neovim, got: %s", parsed.Hits[0].Snippet)
	}
}

func TestMemoryGetTool_Execute(t *testing.T) {
	workspace := t.TempDir()
	ms := NewMemoryStore(workspace)
	ms.SetVectorSettings(MemoryVectorSettings{
		Enabled:         true,
		Dimensions:      128,
		TopK:            5,
		MinScore:        0.01,
		MaxContextChars: 1200,
		RecentDailyDays: 7,
	})

	if err := os.MkdirAll(filepath.Join(workspace, "memory"), 0o755); err != nil {
		t.Fatalf("mkdir memory dir: %v", err)
	}
	content := `# MEMORY

## Long-term Facts
- Favorite editor: neovim
`
	if err := os.WriteFile(filepath.Join(workspace, "memory", "MEMORY.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write memory: %v", err)
	}

	search := NewMemorySearchTool(ms, 3, 0.01)
	searchResult := search.Execute(context.Background(), map[string]any{
		"query": "favorite editor",
	})
	if searchResult.IsError {
		t.Fatalf("memory_search failed: %s", searchResult.ForLLM)
	}

	get := NewMemoryGetTool(ms)
	getResult := get.Execute(context.Background(), map[string]any{
		"source": "MEMORY.md#facts",
	})
	if getResult.IsError {
		t.Fatalf("memory_get failed: %s", getResult.ForLLM)
	}

	var parsed struct {
		Kind  string `json:"kind"`
		Found bool   `json:"found"`
		Hit   struct {
			ID      string `json:"id"`
			Source  string `json:"source"`
			Content string `json:"content"`
		} `json:"hit"`
	}
	if err := json.Unmarshal([]byte(getResult.ForLLM), &parsed); err != nil {
		t.Fatalf("expected JSON tool output, got unmarshal error: %v\nraw=%s", err, getResult.ForLLM)
	}
	if parsed.Kind != "memory_get_result" {
		t.Fatalf("kind = %q, want %q", parsed.Kind, "memory_get_result")
	}
	if !parsed.Found {
		t.Fatalf("expected found=true")
	}
	if parsed.Hit.Source != "MEMORY.md#facts" {
		t.Fatalf("unexpected hit source: %q", parsed.Hit.Source)
	}
	if !strings.Contains(strings.ToLower(parsed.Hit.Content), "neovim") {
		t.Fatalf("expected memory_get content to include neovim, got: %s", parsed.Hit.Content)
	}
}
