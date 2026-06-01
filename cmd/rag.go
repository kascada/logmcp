package cmd

import (
	"context"
	"embed"
	"fmt"

	"github.com/kleist-dev/logmcp/internal/config"
	"github.com/kleist-dev/logmcp/internal/rag"
	"github.com/spf13/cobra"
)

func newRagCmd(docsFS embed.FS) *cobra.Command {
	ragCmd := &cobra.Command{
		Use:   "rag",
		Short: "RAG index management",
	}
	ragCmd.AddCommand(newRagIndexCmd(docsFS))
	ragCmd.AddCommand(newRagQueryCmd())
	return ragCmd
}

func newRagIndexCmd(docsFS embed.FS) *cobra.Command {
	var source string

	cmd := &cobra.Command{
		Use:   "index",
		Short: "Index documents into the RAG vector store",
		Long: `Reads all configured document sources, chunks them, generates embeddings
via Ollama, and writes the vectors into Redis. Existing entries for each
source are replaced.

The built-in LogMCP docs are always indexed as source "logmcp" unless
disabled via 'rag.builtin: false' in the config.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return ragIndex(docsFS, source)
		},
	}

	cmd.Flags().StringVar(&source, "source", "", "Index only this source by name (default: all)")
	return cmd
}

func newRagQueryCmd() *cobra.Command {
	var (
		source string
		topK   int
	)
	cmd := &cobra.Command{
		Use:   "query <text>",
		Short: "Search the RAG index (for testing)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(config.DefaultConfigPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			if cfg.RAG == nil {
				return fmt.Errorf("RAG is not configured")
			}
			sources := make([]string, 0)
			if cfg.RAG.BuiltinEnabled() {
				sources = append(sources, "logmcp")
			}
			for _, s := range cfg.RAG.Sources {
				sources = append(sources, s.Name)
			}
			querier := &rag.Querier{
				Ollama:  rag.NewOllamaClient(cfg.RAG.OllamaURL, cfg.RAG.EmbeddingModel),
				Store:   rag.NewStore(cfg.RAG.RedisAddr),
				Sources: sources,
			}
			results, err := querier.Search(context.Background(), args[0], source, topK)
			if err != nil {
				return fmt.Errorf("search: %w", err)
			}
			fmt.Printf("%d result(s)\n\n", len(results))
			for i, r := range results {
				fmt.Printf("[%d] score=%.4f  source=%s  file=%s\n    title: %s\n    text: %.120s...\n\n",
					i+1, r.Score, r.Source, r.File, r.Title, r.Text)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&source, "source", "", "Restrict to this source")
	cmd.Flags().IntVar(&topK, "top", 5, "Number of results")
	return cmd
}

func ragIndex(docsFS embed.FS, onlySource string) error {
	cfg, err := config.Load(config.DefaultConfigPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	if cfg.RAG == nil {
		return fmt.Errorf("RAG is not configured (no 'rag:' block in %s)", config.DefaultConfigPath)
	}

	ollama := rag.NewOllamaClient(cfg.RAG.OllamaURL, cfg.RAG.EmbeddingModel)
	store := rag.NewStore(cfg.RAG.RedisAddr)
	ctx := context.Background()

	fmt.Printf("Ollama:  %s  (model: %s)\n", cfg.RAG.OllamaURL, cfg.RAG.EmbeddingModel)
	fmt.Printf("Redis:   %s\n\n", cfg.RAG.RedisAddr)

	if err := ollama.Ping(ctx); err != nil {
		return fmt.Errorf("ollama: %w", err)
	}
	if err := store.Ping(ctx); err != nil {
		return fmt.Errorf("redis: %w", err)
	}

	indexer := &rag.Indexer{
		Ollama: ollama,
		Store:  store,
		DocsFS: docsFS,
		Cfg:    cfg.RAG,
	}

	if onlySource != "" {
		return indexSource(ctx, indexer, cfg.RAG, onlySource)
	}
	return indexAll(ctx, indexer, cfg.RAG)
}

func indexAll(ctx context.Context, indexer *rag.Indexer, cfg *config.RAGConfig) error {
	if cfg.BuiltinEnabled() {
		fmt.Print("Indexing built-in docs (logmcp)... ")
		if err := indexer.IndexBuiltin(ctx); err != nil {
			return err
		}
		fmt.Println("done")
	}
	for _, src := range cfg.Sources {
		fmt.Printf("Indexing source %q (%s)... ", src.Name, src.Path)
		if err := indexer.IndexSource(ctx, src); err != nil {
			return err
		}
		fmt.Println("done")
	}
	fmt.Println("\nIndexing complete.")
	return nil
}

func indexSource(ctx context.Context, indexer *rag.Indexer, cfg *config.RAGConfig, name string) error {
	if name == "logmcp" {
		if !cfg.BuiltinEnabled() {
			return fmt.Errorf("source %q is disabled via 'rag.builtin: false'", name)
		}
		fmt.Print("Indexing built-in docs (logmcp)... ")
		if err := indexer.IndexBuiltin(ctx); err != nil {
			return err
		}
		fmt.Println("done")
		return nil
	}
	for _, src := range cfg.Sources {
		if src.Name == name {
			fmt.Printf("Indexing source %q (%s)... ", src.Name, src.Path)
			if err := indexer.IndexSource(ctx, src); err != nil {
				return err
			}
			fmt.Println("done")
			return nil
		}
	}
	return fmt.Errorf("source %q not found in config", name)
}
