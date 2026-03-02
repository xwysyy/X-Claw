package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

type openAICompatMemoryVectorEmbedder struct {
	apiKey    string
	apiBase   string
	model     string
	batchSize int
	timeout   time.Duration
	client    *http.Client
}

func newOpenAICompatMemoryVectorEmbedder(settings MemoryVectorEmbeddingSettings) memoryVectorEmbedder {
	settings = normalizeMemoryVectorEmbeddingSettings(settings)

	timeout := time.Duration(settings.RequestTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	client := &http.Client{Timeout: timeout}
	if settings.Proxy != "" {
		if parsed, err := url.Parse(settings.Proxy); err == nil {
			client.Transport = &http.Transport{
				Proxy: http.ProxyURL(parsed),
			}
		}
	}

	return &openAICompatMemoryVectorEmbedder{
		apiKey:    settings.APIKey,
		apiBase:   strings.TrimRight(settings.APIBase, "/"),
		model:     settings.Model,
		batchSize: settings.BatchSize,
		timeout:   timeout,
		client:    client,
	}
}

func (e *openAICompatMemoryVectorEmbedder) Kind() string { return "openai_compat" }

func (e *openAICompatMemoryVectorEmbedder) Signature() string {
	base := strings.TrimRight(strings.TrimSpace(e.apiBase), "/")
	model := strings.TrimSpace(e.model)
	return fmt.Sprintf("openai_compat:model=%s;base=%s", model, base)
}

func (e *openAICompatMemoryVectorEmbedder) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	if e == nil || e.client == nil {
		return nil, fmt.Errorf("embedding client not configured")
	}
	if strings.TrimSpace(e.apiBase) == "" {
		return nil, fmt.Errorf("embedding api_base not configured")
	}
	if strings.TrimSpace(e.model) == "" {
		return nil, fmt.Errorf("embedding model not configured")
	}

	batchSize := e.batchSize
	if batchSize <= 0 {
		batchSize = 64
	}

	out := make([][]float32, 0, len(inputs))
	for start := 0; start < len(inputs); start += batchSize {
		end := start + batchSize
		if end > len(inputs) {
			end = len(inputs)
		}

		vecs, err := e.embedBatch(ctx, inputs[start:end])
		if err != nil {
			return nil, err
		}
		out = append(out, vecs...)
	}
	return out, nil
}

func (e *openAICompatMemoryVectorEmbedder) embedBatch(ctx context.Context, inputs []string) ([][]float32, error) {
	type embeddingRequest struct {
		Model string   `json:"model"`
		Input []string `json:"input"`
	}
	reqBody := embeddingRequest{
		Model: e.model,
		Input: inputs,
	}
	raw, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal embeddings request: %w", err)
	}

	if _, ok := ctx.Deadline(); !ok && e.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.timeout)
		defer cancel()
	}

	endpoint := strings.TrimRight(e.apiBase, "/") + "/embeddings"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("create embeddings request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(e.apiKey) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(e.apiKey))
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embeddings request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read embeddings response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		msg := string(body)
		if len(msg) > 1200 {
			msg = msg[:1200] + "... (truncated)"
		}
		return nil, fmt.Errorf("embeddings API error: status=%d body=%s", resp.StatusCode, msg)
	}

	type embeddingItem struct {
		Embedding []float64 `json:"embedding"`
		Index     int       `json:"index"`
	}
	var parsed struct {
		Data []embeddingItem `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("unmarshal embeddings response: %w", err)
	}
	if len(parsed.Data) == 0 {
		return nil, fmt.Errorf("embeddings response missing data")
	}

	// Some providers may return items out of order. Preserve original input order via index.
	items := make([]embeddingItem, 0, len(parsed.Data))
	for _, item := range parsed.Data {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Index < items[j].Index })

	vecs := make([][]float32, len(inputs))
	for _, item := range items {
		if item.Index < 0 || item.Index >= len(vecs) {
			continue
		}
		if len(item.Embedding) == 0 {
			continue
		}
		out := make([]float32, len(item.Embedding))
		for i := range item.Embedding {
			out[i] = float32(item.Embedding[i])
		}
		vecs[item.Index] = out
	}

	for i := range vecs {
		if len(vecs[i]) == 0 {
			return nil, fmt.Errorf("embeddings response missing item for index %d", i)
		}
	}

	return vecs, nil
}
