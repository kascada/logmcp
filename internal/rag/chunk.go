package rag

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Chunk is a segment of a document stored in the vector index.
type Chunk struct {
	ID     string   // unique element name in Redis ("source/file/N")
	Source string   // source name ("logmcp", "switchboard", ...)
	File   string   // relative file path within the source
	Title  string   // document title (from frontmatter or first H1)
	Text   string   // chunk text (heading + body)
	Tags   []string // optional tags from frontmatter
}

// SearchResult is a Chunk paired with a similarity score.
type SearchResult struct {
	Chunk
	Score float64
}

type frontmatter struct {
	Title string   `yaml:"title"`
	Tags  []string `yaml:"tags"`
}

// ChunkMarkdown splits a Markdown document into chunks by ## sections.
// Each section becomes one chunk. If the document has no ## headings,
// the whole content is one chunk.
func ChunkMarkdown(source, file, content string) []Chunk {
	fmTitle, tags, body := parseFrontmatter(content)
	title := fmTitle
	if title == "" {
		title = extractH1(body)
	}
	if title == "" {
		title = file
	}

	sections := splitSections(body)
	chunks := make([]Chunk, 0, len(sections))
	for i, text := range sections {
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		chunks = append(chunks, Chunk{
			ID:     fmt.Sprintf("%s/%s/%d", source, file, i),
			Source: source,
			File:   file,
			Title:  title,
			Text:   text,
			Tags:   tags,
		})
	}
	return chunks
}

func parseFrontmatter(content string) (title string, tags []string, body string) {
	if !strings.HasPrefix(content, "---\n") {
		return "", nil, content
	}
	rest := content[4:]
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		return "", nil, content
	}
	var fm frontmatter
	_ = yaml.Unmarshal([]byte(rest[:end]), &fm)
	return fm.Title, fm.Tags, rest[end+5:]
}

func extractH1(content string) string {
	for _, line := range strings.SplitN(content, "\n", 20) {
		if strings.HasPrefix(line, "# ") && !strings.HasPrefix(line, "## ") {
			return strings.TrimPrefix(line, "# ")
		}
	}
	return ""
}

// maxChunkRunes is the maximum size of a single chunk sent to the embedding model.
// nomic-embed-text has a default context of ~2048 tokens; ~4 chars/token → ~8000 chars.
// We stay well below that to leave room for non-ASCII characters.
const maxChunkRunes = 6000

// splitSections splits content by ## headings, keeping each heading with its body.
// Sections that exceed maxChunkRunes are further split by paragraph boundaries.
func splitSections(content string) []string {
	lines := strings.Split(content, "\n")
	var sections []string
	var current []string

	flush := func() {
		if len(current) == 0 {
			return
		}
		text := strings.Join(current, "\n")
		sections = append(sections, splitLargeSection(text)...)
		current = nil
	}

	for _, line := range lines {
		if strings.HasPrefix(line, "## ") && len(current) > 0 {
			flush()
			current = []string{line}
		} else {
			current = append(current, line)
		}
	}
	flush()
	return sections
}

// splitLargeSection splits a section by paragraph boundaries when it exceeds
// maxChunkRunes, falling back to fixed-size rune splits if paragraphs are
// themselves too large.
func splitLargeSection(text string) []string {
	if len([]rune(text)) <= maxChunkRunes {
		return []string{text}
	}
	paragraphs := strings.Split(text, "\n\n")
	var result []string
	var acc strings.Builder
	for _, para := range paragraphs {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		if acc.Len() > 0 && len([]rune(acc.String()))+len([]rune(para))+2 > maxChunkRunes {
			result = append(result, acc.String())
			acc.Reset()
		}
		if acc.Len() > 0 {
			acc.WriteString("\n\n")
		}
		acc.WriteString(para)
	}
	if acc.Len() > 0 {
		result = append(result, acc.String())
	}
	if len(result) == 0 {
		return splitByRunes(text, maxChunkRunes)
	}
	return result
}

// splitByRunes splits text into chunks of at most size runes.
func splitByRunes(text string, size int) []string {
	runes := []rune(text)
	var result []string
	for i := 0; i < len(runes); i += size {
		end := i + size
		if end > len(runes) {
			end = len(runes)
		}
		result = append(result, string(runes[i:end]))
	}
	return result
}
