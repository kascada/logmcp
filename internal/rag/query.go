package rag

import (
	"cmp"
	"context"
	"slices"
)

// Querier performs semantic search over the RAG index.
type Querier struct {
	Ollama  *OllamaClient
	Store   *Store
	Sources []string // all indexed source names
}

// Search embeds the query and returns the topK most relevant chunks.
// If source is non-empty, only that source is searched.
// Otherwise all sources are searched and results are merged by score.
func (q *Querier) Search(ctx context.Context, query, source string, topK int) ([]SearchResult, error) {
	vector, err := q.Ollama.Embed(ctx, query)
	if err != nil {
		return nil, err
	}

	if source != "" {
		return q.Store.Search(ctx, source, vector, topK)
	}

	var all []SearchResult
	for _, src := range q.Sources {
		results, err := q.Store.Search(ctx, src, vector, topK)
		if err != nil {
			// Source may not be indexed yet — skip silently.
			continue
		}
		all = append(all, results...)
	}

	slices.SortFunc(all, func(a, b SearchResult) int {
		return cmp.Compare(b.Score, a.Score) // descending
	})
	if len(all) > topK {
		all = all[:topK]
	}
	return all, nil
}
