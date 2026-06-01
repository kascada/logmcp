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

// splitSections splits content by ## headings, keeping each heading with its body.
func splitSections(content string) []string {
	lines := strings.Split(content, "\n")
	var sections []string
	var current []string

	for _, line := range lines {
		if strings.HasPrefix(line, "## ") && len(current) > 0 {
			sections = append(sections, strings.Join(current, "\n"))
			current = []string{line}
		} else {
			current = append(current, line)
		}
	}
	if len(current) > 0 {
		sections = append(sections, strings.Join(current, "\n"))
	}
	return sections
}
