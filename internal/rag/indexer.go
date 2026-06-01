package rag

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/kleist-dev/logmcp/internal/config"
)

// Indexer orchestrates document indexing into the vector store.
type Indexer struct {
	Ollama  *OllamaClient
	Store   *Store
	DocsFS  embed.FS
	Cfg     *config.RAGConfig
}

// Index re-indexes all sources. Each source is fully replaced.
// The built-in LogMCP docs are always indexed first when Builtin is enabled.
func (ix *Indexer) Index(ctx context.Context) error {
	if ix.Cfg.BuiltinEnabled() {
		if err := ix.IndexBuiltin(ctx); err != nil {
			return fmt.Errorf("built-in docs: %w", err)
		}
	}
	for _, src := range ix.Cfg.Sources {
		if err := ix.IndexSource(ctx, src); err != nil {
			return fmt.Errorf("source %q: %w", src.Name, err)
		}
	}
	return nil
}

// IndexBuiltin indexes the embedded LogMCP docs as source "logmcp".
func (ix *Indexer) IndexBuiltin(ctx context.Context) error {
	return ix.indexEmbedded(ctx)
}

// IndexSource indexes a single filesystem source.
func (ix *Indexer) IndexSource(ctx context.Context, src config.RAGSource) error {
	return ix.indexDir(ctx, src.Name, src.Path)
}

// indexEmbedded indexes the docs/*.md files from the embedded FS.
func (ix *Indexer) indexEmbedded(ctx context.Context) error {
	if err := ix.Store.Drop(ctx, "logmcp"); err != nil {
		return err
	}
	return fs.WalkDir(ix.DocsFS, "docs", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !isIndexable(path) {
			return nil
		}
		data, err := ix.DocsFS.ReadFile(path)
		if err != nil {
			return err
		}
		return ix.indexContent(ctx, "logmcp", path, string(data))
	})
}

// indexDir indexes all supported files from a filesystem directory.
func (ix *Indexer) indexDir(ctx context.Context, source, dir string) error {
	if err := ix.Store.Drop(ctx, source); err != nil {
		return err
	}
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !isIndexable(path) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		return ix.indexContent(ctx, source, rel, string(data))
	})
}

func (ix *Indexer) indexContent(ctx context.Context, source, file, content string) error {
	chunks := ChunkMarkdown(source, file, content)
	if len(chunks) == 0 {
		return nil
	}

	vectors := make([][]float32, len(chunks))
	for i, chunk := range chunks {
		vec, err := ix.Ollama.Embed(ctx, chunk.Text)
		if err != nil {
			return fmt.Errorf("embedding %s: %w", file, err)
		}
		vectors[i] = vec
	}

	return ix.Store.Add(ctx, source, chunks, vectors)
}

func isIndexable(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".txt":
		return true
	}
	return false
}
