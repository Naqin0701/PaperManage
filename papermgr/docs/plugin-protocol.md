# PaperManage plugin protocol (v1)

A plugin is a directory under `~/.config/papermgr/plugins/<name>/` containing
`plugin.toml` and an executable. It is spawned as a child process and speaks
newline-delimited JSON-RPC 2.0 on stdin/stdout. Logs go to stderr.

## Manifest

```toml
name = "crossref"
version = "0.1.0"
protocol = "papermgr-plugin/v1"    # required, exact match
capabilities = ["metadata"]        # metadata | extract | convert | capture
entry = ["./bin/fetch-crossref"]   # relative to the plugin directory or on PATH
priority = 100                     # higher wins field conflicts
timeout_seconds = 20               # optional; capability defaults apply
requires = ["python3"]             # external binaries checked before loading
[config]
mailto = "user@example.com"
```

Library-level `config.toml` may override enablement and config:

```toml
[plugins.crossref]
enabled = true
[plugins.crossref.config]
mailto = "real@example.com"
```

## Handshake

Every invocation starts with `init`; the plugin must answer with the matching
protocol or it is rejected.

```json
{"jsonrpc":"2.0","id":1,"method":"init","params":{"library_root":"/home/u/papers","protocol":"papermgr-plugin/v1","config":{}}}
{"jsonrpc":"2.0","id":1,"result":{"ok":true,"protocol":"papermgr-plugin/v1"}}
```

## Methods

| Method | Capability | Params | Result |
|---|---|---|---|
| `health` | all | `{}` | `{ok, missing: []}` |
| `metadata.fetch` | metadata | `{pdf_path, sha256, hints}` | `{candidates:[{fields, confidence, source}]}` |
| `extract.text` | extract | `{pdf_path}` | `{text, pages, method}` |
| `convert.markdown` | convert | `{pdf_path, out_dir}` | `{files: [], method}` |
| `capture.web` | capture | `{url, out_dir}` | `{files: [], url}` |

`hints` may include `filename`, `title_guess`, `year`, `doi`, `arxiv_id`.

`fields` uses the `meta.json` vocabulary: `title`, `authors`, `year`, `venue`
(`{name, short, kind, volume, issue, pages, publisher}`), `doi`, `arxiv_id`,
`url`, `abstract`, `keywords`, `entry_type`, `language`.

Timeouts: metadata 30s, extract 120s, convert 1800s, capture 60s. A timeout or
crash is a failure for that plugin only; the core degrades (metadata lowers
confidence, extraction records an error, conversion is logged).

## Bundled plugins

| Plugin | Priority | Lookup order | Notes |
|---|---|---|---|
| `plugins/openalex` | 110 | DOI → arXiv ID → title search | ORCIDs, affiliations, abstracts, keywords, venue and pages |
| `plugins/crossref` | 100 | DOI / arXiv ID → title search | Crossref publisher metadata and arXiv Atom API |

Higher priority plugins are applied first; a later plugin only fills fields
that are still empty. `openalex` accepts `mailto`, `api_key` (falls back to
`$OPENALEX_API_KEY`) and `timeout_seconds` in its plugin config. The API key is
optional: keyless requests work on OpenAlex's free daily budget.

```toml
[plugins.openalex.config]
mailto = "you@example.com"
api_key = "..."
```

## Minimal shell plugin

```sh
#!/bin/sh
while IFS= read -r line; do
  case "$line" in
    *'"init"'*)
      echo '{"jsonrpc":"2.0","id":1,"result":{"ok":true,"protocol":"papermgr-plugin/v1"}}' ;;
    *'"metadata.fetch"'*)
      echo '{"jsonrpc":"2.0","id":2,"result":{"candidates":[{"fields":{"title":"Example"},"confidence":0.9,"source":"example"}]}}' ;;
  esac
done
```

Confidence drives ingest review: `≥0.9` with a DOI is accepted directly,
`0.7–0.9` is accepted but queued for review, and `<0.7` is never allowed to
overwrite metadata derived from the PDF itself (it is still retained as a
review candidate).
