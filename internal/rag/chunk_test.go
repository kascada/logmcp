package rag

import (
	"strings"
	"testing"
)

func TestChunkMarkdown_SmallFile(t *testing.T) {
	content := "# Title\n\n## Section A\n\nSome text.\n\n## Section B\n\nMore text.\n"
	chunks := ChunkMarkdown("src", "doc.md", content)
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks (preamble + 2 sections), got %d", len(chunks))
	}
	if chunks[0].Title != "Title" {
		t.Errorf("unexpected title: %q", chunks[0].Title)
	}
}

func TestChunkMarkdown_LargeSection(t *testing.T) {
	// Build a single section that exceeds maxChunkRunes.
	para := strings.Repeat("x", 1000)
	var sb strings.Builder
	sb.WriteString("# Big Doc\n\n")
	for i := 0; i < 10; i++ {
		sb.WriteString(para)
		sb.WriteString("\n\n")
	}
	content := sb.String() // ~10000 chars in one implicit section

	chunks := ChunkMarkdown("src", "big.md", content)
	if len(chunks) < 2 {
		t.Fatalf("expected large section to be split into multiple chunks, got %d", len(chunks))
	}
	for _, c := range chunks {
		if len([]rune(c.Text)) > maxChunkRunes {
			t.Errorf("chunk exceeds maxChunkRunes (%d): len=%d", maxChunkRunes, len([]rune(c.Text)))
		}
	}
}

func TestChunkMarkdown_NoHeadings(t *testing.T) {
	// File with no ## headings, content small enough for one chunk.
	content := "Just some prose without any headings.\n"
	chunks := ChunkMarkdown("src", "plain.md", content)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
}

func TestSplitByRunes(t *testing.T) {
	text := strings.Repeat("a", 15)
	parts := splitByRunes(text, 6)
	if len(parts) != 3 {
		t.Fatalf("expected 3 parts, got %d", len(parts))
	}
	for _, p := range parts {
		if len([]rune(p)) > 6 {
			t.Errorf("part exceeds size 6: %q", p)
		}
	}
}
