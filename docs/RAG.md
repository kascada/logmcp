# LogMCP — RAG (Retrieval-Augmented Generation)

LogMCP can act as a retrieval backend for AI clients. When configured, it indexes local documents into a Redis vector store using Ollama embeddings, and exposes a `rag_query` MCP tool that returns the most relevant chunks for a given question.

The AI client (Claude Code, or any MCP-capable LLM) uses `rag_query` to fetch context and then generates the answer itself. LogMCP handles only the retrieval side — no local LLM required.

**Requirements:**

- Ollama running with an embedding model pulled (e.g. `nomic-embed-text`)
- Redis with vectorset support (Redis 8+)
- RAG is **opt-in**: if the `rag:` block is absent from the config, no RAG tools are registered and nothing is shown to AI clients

---

## Configuration

Add a `rag:` block to `/etc/logmcp/config.yaml`:

```yaml
rag:
  ollama_url: http://127.0.0.1:11434
  embedding_model: nomic-embed-text
  redis_addr: 127.0.0.1:6379
  sources:
    - name: switchboard
      path: /opt/switchboard/docs
```

The LogMCP built-in documentation (the same content served via `logmcp://docs/*` MCP resources) is **always indexed automatically** as source `logmcp` — no entry in `sources` is needed or allowed for it.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `ollama_url` | string | `http://127.0.0.1:11434` | Base URL of the Ollama HTTP API. Change when Ollama runs on a different host — the admin is responsible for port forwarding and access control. |
| `embedding_model` | string | `nomic-embed-text` | Ollama model used for embeddings. Must be pulled on the Ollama server before indexing (`ollama pull nomic-embed-text`). |
| `redis_addr` | string | `127.0.0.1:6379` | Redis server address. Expects a local Redis instance with no authentication. |
| `sources` | list | `[]` | Additional document sources to index. Can be omitted if only the built-in LogMCP docs are needed. |

### `rag.sources[]`

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | **required** — Identifier for this source. Used as a namespace in the vector store and returned with search results. Must be unique. The name `logmcp` is reserved for built-in docs. |
| `path` | string | **required** — Directory to scan for documents. Scanned recursively. |

---

## Document format

Supported file types: `.md`, `.txt`

Files are indexed as-is. YAML frontmatter is optional — when present, `title` and `tags` are stored as metadata alongside the chunks and returned with search results.

```markdown
---
title: How to configure authentication
tags: [auth, config]
---

# Authentication

Content starts here...
```

Without frontmatter, the file is indexed using its path as the title. No special format is required — existing Markdown files work out of the box.

---

## Indexing

Run indexing manually from the CLI:

```bash
logmcp rag index
```

This scans all configured sources, chunks the documents, generates embeddings via Ollama, and writes them to Redis. Existing index entries for each source are replaced.

To index a single source by name:

```bash
logmcp rag index --source logmcp-docs
```

Indexing reads the RAG config from the standard config file (`/etc/logmcp/config.yaml`). Run it after adding or updating documents. There is no automatic re-indexing on file change.

---

## MCP tools

When `rag:` is configured, LogMCP registers the following MCP tool:

### `rag_query`

Searches the vector store for chunks relevant to the given question and returns them as a list of text passages with source metadata.

**Scope required:** `logmcp:read`

**Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `query` | string | The question or search text to find relevant context for |
| `top_k` | int | Number of results to return (default: 5) |
| `source` | string | Restrict results to a single source by name (optional) |

**Returns:** List of matching chunks, each with `text`, `source`, `title`, and `score`.

---

## Ansible deployment

The Ollama service and embedding model are provisioned via the `ollama` Ansible role. LogMCP itself does not install or manage Ollama.

Minimum tasks in the `ollama` role:

```yaml
- name: Install Ollama
  shell: curl -fsSL https://ollama.com/install.sh | sh

- name: Enable and start ollama
  systemd:
    name: ollama
    state: started
    enabled: yes

- name: Pull embedding model
  command: ollama pull nomic-embed-text
  become_user: ollama
```

Ollama listens on `127.0.0.1:11434` by default — no firewall changes required for local use. To allow access from another host, set `OLLAMA_HOST=0.0.0.0` in the systemd environment and configure network access control separately.

---

## check_environment

When RAG is configured, `check_environment` includes the following additional checks:

- Ollama reachable at the configured URL
- Embedding model available on the Ollama server
- Redis reachable at the configured address

If any of these fail, the check reports the specific error. RAG tools remain registered but will return errors on use until the underlying issue is resolved.
