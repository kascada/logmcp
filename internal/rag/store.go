package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const ragTTL = 30 * 24 * time.Hour

// Store handles Redis vectorset operations for the RAG index.
//
// Key layout (prefix = redis.key_prefix from config, default "logmcp"):
//
//	{prefix}:rag:{source}        — vectorset (element = chunk ID)
//	{prefix}:rag:idx:{source}    — Set of chunk IDs (for cleanup on re-index)
//	{prefix}:rag:meta:{chunkID}  — Hash with chunk metadata and text
type Store struct {
	client    *redis.Client
	keyPrefix string // e.g. "logmcp:rag"
}

// NewStore creates a Store backed by the given Redis address.
func NewStore(addr, password, keyPrefix string) *Store {
	return &Store{
		client: redis.NewClient(&redis.Options{
			Addr:     addr,
			Password: password,
		}),
		keyPrefix: keyPrefix + ":rag",
	}
}

func (s *Store) vsKey(source string) string {
	return fmt.Sprintf("%s:%s", s.keyPrefix, source)
}

func (s *Store) idxKey(source string) string {
	return fmt.Sprintf("%s:idx:%s", s.keyPrefix, source)
}

func (s *Store) metaKey(chunkID string) string {
	return fmt.Sprintf("%s:meta:%s", s.keyPrefix, chunkID)
}

// Drop removes all vectorset entries and metadata for a source.
func (s *Store) Drop(ctx context.Context, source string) error {
	ids, err := s.client.SMembers(ctx, s.idxKey(source)).Result()
	if err != nil && err != redis.Nil {
		return err
	}

	pipe := s.client.Pipeline()
	pipe.Del(ctx, s.vsKey(source))
	pipe.Del(ctx, s.idxKey(source))
	for _, id := range ids {
		pipe.Del(ctx, s.metaKey(id))
	}
	_, err = pipe.Exec(ctx)
	return err
}

// Add stores chunks with their embedding vectors into the vectorset.
// Call Drop before Add to replace an existing source index.
func (s *Store) Add(ctx context.Context, source string, chunks []Chunk, vectors [][]float32) error {
	pipe := s.client.Pipeline()
	vsKey := s.vsKey(source)
	idxKey := s.idxKey(source)

	for i, chunk := range chunks {
		vec := vectors[i]

		// VADD key VALUES dim f1 f2 ... fN element
		args := make([]interface{}, 0, 4+len(vec)+1)
		args = append(args, "VADD", vsKey, "VALUES", len(vec))
		for _, f := range vec {
			args = append(args, f)
		}
		args = append(args, chunk.ID)
		pipe.Do(ctx, args...)

		pipe.SAdd(ctx, idxKey, chunk.ID)

		tagsJSON, _ := json.Marshal(chunk.Tags)
		pipe.HSet(ctx, s.metaKey(chunk.ID),
			"source", chunk.Source,
			"file", chunk.File,
			"title", chunk.Title,
			"text", chunk.Text,
			"tags", string(tagsJSON),
		)
		pipe.Expire(ctx, s.metaKey(chunk.ID), ragTTL)
	}

	pipe.Expire(ctx, vsKey, ragTTL)
	pipe.Expire(ctx, idxKey, ragTTL)

	_, err := pipe.Exec(ctx)
	return err
}

// Search finds the topK most similar chunks to the given vector within source.
func (s *Store) Search(ctx context.Context, source string, vector []float32, topK int) ([]SearchResult, error) {
	vsKey := s.vsKey(source)

	// VSIM key VALUES dim f1 f2 ... fN WITHSCORES COUNT n
	// Note: WITHSCORES and COUNT must come AFTER the VALUES block.
	args := make([]interface{}, 0, 7+len(vector))
	args = append(args, "VSIM", vsKey, "VALUES", len(vector))
	for _, f := range vector {
		args = append(args, f)
	}
	args = append(args, "WITHSCORES", "COUNT", topK)

	cmd := s.client.Do(ctx, args...)
	if cmd.Err() != nil {
		return nil, cmd.Err()
	}

	// go-redis v9 uses RESP3 by default; VSIM WITHSCORES returns map[element]score.
	// Handle both RESP3 (map) and RESP2 (flat slice) for portability.
	type hit struct {
		id    string
		score float64
	}
	var hits []hit

	switch val := cmd.Val().(type) {
	case map[interface{}]interface{}:
		for k, v := range val {
			id, _ := k.(string)
			var score float64
			switch sv := v.(type) {
			case float64:
				score = sv
			case string:
				score, _ = strconv.ParseFloat(sv, 64)
			}
			hits = append(hits, hit{id, score})
		}
	case []interface{}:
		for i := 0; i+1 < len(val); i += 2 {
			id, _ := val[i].(string)
			var score float64
			switch sv := val[i+1].(type) {
			case float64:
				score = sv
			case string:
				score, _ = strconv.ParseFloat(sv, 64)
			}
			hits = append(hits, hit{id, score})
		}
	}

	results := make([]SearchResult, 0, len(hits))
	for _, h := range hits {
		meta, err := s.client.HGetAll(ctx, s.metaKey(h.id)).Result()
		if err != nil || len(meta) == 0 {
			continue
		}
		var tags []string
		_ = json.Unmarshal([]byte(meta["tags"]), &tags)
		results = append(results, SearchResult{
			Chunk: Chunk{
				ID:     h.id,
				Source: meta["source"],
				File:   meta["file"],
				Title:  meta["title"],
				Text:   meta["text"],
				Tags:   tags,
			},
			Score: h.score,
		})
	}
	return results, nil
}

// Ping checks that Redis is reachable.
func (s *Store) Ping(ctx context.Context) error {
	return s.client.Ping(ctx).Err()
}
