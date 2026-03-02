package agent

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

func parsePositiveInt(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty int")
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid int %q", s)
	}
	return n, nil
}

func mergeMemoryHits(a, b []MemoryVectorHit, topK int) []MemoryVectorHit {
	if topK <= 0 {
		topK = defaultMemoryVectorTopK
	}

	merged := make(map[string]MemoryVectorHit, len(a)+len(b))

	add := func(hit MemoryVectorHit) {
		hit.Source = strings.TrimSpace(hit.Source)
		hit.Text = strings.TrimSpace(hit.Text)
		if hit.Source == "" || hit.Text == "" {
			return
		}
		if existing, ok := merged[hit.Source]; ok {
			// Keep the higher-scoring hit; if scores tie, prefer the longer snippet.
			if hit.Score > existing.Score || (hit.Score == existing.Score && len(hit.Text) > len(existing.Text)) {
				merged[hit.Source] = hit
			}
			return
		}
		merged[hit.Source] = hit
	}

	for _, hit := range a {
		add(hit)
	}
	for _, hit := range b {
		add(hit)
	}

	out := make([]MemoryVectorHit, 0, len(merged))
	for _, hit := range merged {
		out = append(out, hit)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			return out[i].Source < out[j].Source
		}
		return out[i].Score > out[j].Score
	})
	if len(out) > topK {
		out = out[:topK]
	}
	return out
}

func mergeHybridMemoryHits(ftsHits, vecHits []MemoryVectorHit, topK int, hybrid MemoryHybridSettings) []MemoryVectorHit {
	if topK <= 0 {
		topK = defaultMemoryVectorTopK
	}
	hybrid = normalizeMemoryHybridSettings(hybrid)

	merged := make(map[string]MemoryVectorHit, len(ftsHits)+len(vecHits))

	add := func(hit MemoryVectorHit) {
		hit.Source = strings.TrimSpace(hit.Source)
		hit.Text = strings.TrimSpace(hit.Text)
		if hit.Source == "" || hit.Text == "" {
			return
		}

		if existing, ok := merged[hit.Source]; ok {
			// Merge signals.
			if hit.HasFTS && (!existing.HasFTS || hit.FTSScore > existing.FTSScore) {
				existing.HasFTS = true
				existing.FTSScore = hit.FTSScore
			}
			if hit.HasVector && (!existing.HasVector || hit.VectorScore > existing.VectorScore) {
				existing.HasVector = true
				existing.VectorScore = hit.VectorScore
			}
			// Prefer longer snippet when both refer to same source.
			if len(hit.Text) > len(existing.Text) {
				existing.Text = hit.Text
			}
			merged[hit.Source] = existing
			return
		}

		merged[hit.Source] = hit
	}

	for _, hit := range ftsHits {
		if !hit.HasFTS {
			hit.HasFTS = true
			hit.FTSScore = hit.Score
		}
		hit.MatchKind = "fts"
		add(hit)
	}
	for _, hit := range vecHits {
		if !hit.HasVector {
			hit.HasVector = true
			hit.VectorScore = hit.Score
		}
		hit.MatchKind = "vector"
		add(hit)
	}

	out := make([]MemoryVectorHit, 0, len(merged))
	for _, hit := range merged {
		// Re-score deterministically.
		switch {
		case hit.HasFTS && hit.HasVector:
			hit.MatchKind = "hybrid"
			hit.Score = hybrid.FTSWeight*hit.FTSScore + hybrid.VectorWeight*hit.VectorScore
		case hit.HasFTS:
			hit.MatchKind = "fts"
			hit.Score = hit.FTSScore
		case hit.HasVector:
			hit.MatchKind = "vector"
			hit.Score = hit.VectorScore
		default:
			continue
		}
		out = append(out, hit)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			return out[i].Source < out[j].Source
		}
		return out[i].Score > out[j].Score
	})

	if len(out) > topK {
		out = out[:topK]
	}
	return out
}
