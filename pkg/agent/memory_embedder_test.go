package agent

import "testing"

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
