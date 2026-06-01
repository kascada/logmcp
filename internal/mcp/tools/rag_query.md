# rag_query

Search the indexed knowledge base for text passages relevant to a question
or topic. Returns the most relevant chunks with source name, file, title,
and similarity score.

**Use this tool proactively** whenever the user asks about:
- How something works or is configured on this server
- Documentation, architecture, or operational details
- Anything where the answer might live in project or system documentation

The source `logmcp` (LogMCP documentation — configuration, deployment,
extensions, CLI, Ansible) is always available when this tool is registered.
Additional sources depend on the server configuration — call without a
`source` filter to search across everything, then use the `source` field
in the results to see where answers came from.

If a query returns low-scoring results (below ~0.70), the information is
likely not in the knowledge base. Use `top_k: 3` for focused lookups,
`top_k: 10` when broader coverage is needed.

## Parameters

### query *(required)*

The question or topic to search for. Natural language works best — phrase
it the way you would ask a colleague.

### top_k *(optional, default: 5)*

Number of results to return. Clamped to [1, 20]. Use 3 for a focused
answer, 10 for broader coverage.

### source *(optional)*

Restrict results to a single source by name. When omitted, all indexed
sources are searched and results are merged by similarity score. The
`source` field in each result identifies where the chunk came from.
