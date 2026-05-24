package mcp

import (
	"embed"
	"strings"
)

//go:embed server.md tools/*.md
var toolDescFS embed.FS

type toolDesc struct {
	Description string
	Params      map[string]string
}

func loadServerDesc() string {
	data, err := toolDescFS.ReadFile("server.md")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func loadToolDesc(name string) toolDesc {
	data, err := toolDescFS.ReadFile("tools/" + name + ".md")
	if err != nil {
		return toolDesc{Description: name, Params: map[string]string{}}
	}
	return parseToolDesc(string(data))
}

// parseToolDesc extracts the tool description and per-parameter descriptions
// from a markdown file. The H1 title line is stripped. All other content
// (including the ## Parameters section) becomes the tool description.
// Parameter descriptions are additionally extracted from ### subsections
// under ## Parameters and injected into MCP parameter schemas.
func parseToolDesc(content string) toolDesc {
	td := toolDesc{Params: make(map[string]string)}
	lines := strings.Split(content, "\n")

	var descLines []string
	var inParams bool
	var currentParam string
	var paramBuf []string

	for _, line := range lines {
		// Drop the H1 title — redundant with the tool name.
		if strings.HasPrefix(line, "# ") && !strings.HasPrefix(line, "## ") {
			continue
		}

		descLines = append(descLines, line)

		switch {
		case strings.HasPrefix(line, "## Parameters"):
			inParams = true
		case strings.HasPrefix(line, "## "):
			if inParams && currentParam != "" {
				td.Params[currentParam] = strings.TrimSpace(strings.Join(paramBuf, "\n"))
				paramBuf = nil
				currentParam = ""
			}
			inParams = false
		case inParams && strings.HasPrefix(line, "### "):
			if currentParam != "" {
				td.Params[currentParam] = strings.TrimSpace(strings.Join(paramBuf, "\n"))
			}
			currentParam = strings.TrimPrefix(line, "### ")
			paramBuf = nil
		case inParams && currentParam != "":
			paramBuf = append(paramBuf, line)
		}
	}

	if currentParam != "" {
		td.Params[currentParam] = strings.TrimSpace(strings.Join(paramBuf, "\n"))
	}

	td.Description = strings.TrimSpace(strings.Join(descLines, "\n"))
	return td
}
