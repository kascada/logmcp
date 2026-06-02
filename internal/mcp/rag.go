package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/kleist-dev/logmcp/internal/config"
	"github.com/kleist-dev/logmcp/internal/rag"
	"github.com/mark3labs/mcp-go/mcp"
)

// ragQueryResult is the JSON response for a single rag_query result.
type ragQueryResult struct {
	Source string  `json:"source"`
	File   string  `json:"file"`
	Title  string  `json:"title"`
	Score  float64 `json:"score"`
	Text   string  `json:"text"`
}

// handleRagQuery implements the rag_query MCP tool.
func (s *Server) handleRagQuery(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, err := req.RequireString("query")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	source := req.GetString("source", "")
	topK := int(req.GetInt("top_k", 5))
	if topK < 1 {
		topK = 1
	}
	if topK > 20 {
		topK = 20
	}

	s.mu.RLock()
	querier := s.ragQuerier
	s.mu.RUnlock()

	if querier == nil {
		return mcp.NewToolResultError("RAG is not configured on this server"), nil
	}

	results, err := querier.Search(ctx, query, source, topK)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("rag search: %v", err)), nil
	}

	out := make([]ragQueryResult, 0, len(results))
	for _, r := range results {
		out = append(out, ragQueryResult{
			Source: r.Source,
			File:   r.File,
			Title:  r.Title,
			Score:  r.Score,
			Text:   r.Text,
		})
	}

	data, err := json.Marshal(out)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("serialising results: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

// buildQuerier creates a Querier from the RAG config, or returns nil when RAG is disabled.
func buildQuerier(cfg *config.Config) *rag.Querier {
	if cfg.RAG == nil {
		return nil
	}
	sources := make([]string, 0, len(cfg.RAG.Sources)+1)
	if cfg.RAG.BuiltinEnabled() {
		sources = append(sources, "logmcp")
	}
	for _, src := range cfg.RAG.Sources {
		sources = append(sources, src.Name)
	}
	return &rag.Querier{
		Ollama:  rag.NewOllamaClient(cfg.RAG.OllamaURL, cfg.RAG.EmbeddingModel),
		Store:   rag.NewStore(cfg.Redis.Addr, cfg.Redis.Password, cfg.Redis.KeyPrefix),
		Sources: sources,
	}
}
