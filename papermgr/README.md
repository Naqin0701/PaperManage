# PaperManage (`papermgr`)

A tag-centric, PDF-first, single-user paper manager for the terminal. The TUI is
the main interface; the CLI is a first-class citizen. Every paper is a plain
directory, `meta.json` is the source of truth, and `library.sqlite` is a
throw-away search index.

```text
 papermgr  ~/papermgr-demo/library                              4 results · index fresh
 TAGS                 9 │▏RESULTS                      4 │ DETAIL
▸ All papers          4 │     unpublished notes           │ Attention Is All You Need
  Pending review (1)  1 │    1997 Long Short-Term Memory  │ vaswani-2017-attention-all-you
  ml                  3 │    2020 Retrieval-Augmented …   │ Ashish Vaswani, Noam Shazeer, …
    llm               3 │▸   2017 Attention Is All You…   │ 2017 · arXiv · preprint
      transformer     1 │                                 │ #ml/llm/transformer #status/read
  status              4 │                                 │ review done
 1 tags   2 results   3 detail   tab cycle        / search   ? help   q quit
```

The live 150×44 capture is in [`docs/tui-screenshot.txt`](docs/tui-screenshot.txt).

## Why

- **Tags are the only taxonomy.** Hierarchy is encoded in the tag itself
  (`ml/llm/rag`); state is a namespace (`status/reading`, `status/read`).
  Parent tags are implicit and are never written to disk.
- **PDF is the only entry point.** There is no DOI/arXiv/BibTeX import path, so
  every entry has a full text you can open offline.
- **The filesystem is the truth.** Delete `library.sqlite` and lose nothing.
  Notes are Markdown/TeX/Typst, metadata is readable JSON, and the whole library
  can be versioned with git or opened by Obsidian.

## Requirements

- Go 1.26+ (build only; the released binary is a single static executable, no CGO)
- `pdftotext` from poppler-utils for full-text search (optional; the library
  remains usable without it, and `content:` searches explain what is missing)
- Python 3 for the bundled Crossref/arXiv metadata plugin (optional)

## Build and install

```sh
make build          # -> ./bin/papermgr
make test           # unit + integration tests
make install        # go install ./cmd/papermgr
```

Manual build:

```sh
CGO_ENABLED=0 go build -o bin/papermgr ./cmd/papermgr
```

## Quick start

```sh
# 1. Create a library (or `papermgr init ~/papers`)
papermgr init ~/papers

# 2. Install the bundled metadata plugin (optional but recommended)
papermgr plugin install ./plugins/crossref

# 3. Ingest PDFs; metadata plugins fill title/authors/DOI and set confidence
papermgr add ~/Downloads/*.pdf --tag ml/llm --tag status/to-read --yes

# 4. Browse, search, tag, open notes — all in the TUI
papermgr tui

# 5. Export for LaTeX
papermgr bib export
```

The default library comes from (in order) `--library`, `$PAPERMGR_LIBRARY`, or
`~/.config/papermgr/config.toml`. Two demo helpers are included:

```sh
./scripts/setup-demo.sh           # stable demo at ~/papermgr-demo + default config
./scripts/setup-demo.sh --reset   # rebuild it from scratch
./scripts/demo.sh                 # isolated throw-away demo under /tmp
```

`setup-demo.sh` creates four papers (three resolved via Crossref/arXiv, one
left in the review queue), a main note, a supplement, a saved query, installs
the bundled Crossref plugin, and copies the binary to `~/.local/bin/papermgr`.
It backs up any previous `~/.config/papermgr/config.toml` first.

## TUI

Three panes: tag tree, result list, detail. Everything is keyboard driven.

| Key | Action |
|---|---|
| `/` | search prompt |
| `:` | command prompt (`:tag add ml/llm`, `:reindex --rebuild`, …) |
| `?` | full help |
| `1` `2` `3` | focus tag tree / result list / detail (btop-style) |
| `Tab` / `Shift+Tab` | cycle focus through the three panes |
| `j k` `g G` | move / scroll the focused pane; first / last |
| `space` | mark a paper; in the tag pane, collapse/expand a tag |
| `t` | add/remove tags on selection (`ml/llm -status/skim`) |
| `x` | rename to the template-suggested citekey |
| `o` / `e` | open PDF / note in external programs |
| `c` | convert PDF to Markdown (convert plugin) |
| `d` | move to `.trash` (confirmation, never immediate deletion) |
| `p` | pending metadata review panel (per-field accept / keep) |
| `L` | structured log panel, including plugin stderr |
| `b` | background task panel |
| `R` | reindex changes detected by the stat-only scan |
| `s` | cycle sort: relevance, year↓, added↓, title↑, year↑ |
| `q` | quit |

The colour scheme is **Tokyo Night** (Night on dark terminals, Day on light
terminals), foreground-only: `#c0caf5` text, `#7aa2f7` accent, `#7dcfff`
tags/links and matching green/yellow/red semantics. No cell background is
painted, so Ghostty transparency and blur remain visible through the whole UI.

The tag pane uses a Logseq-style outline: `▾`/`▸` collapse parents, `•` marks
leaves, and faint `│` guides show ancestry. Press `space` on a parent tag to
collapse or expand it.

The layout is intentionally flat: no full-width colour bars, no rounded boxes,
no filled selection block. The active pane is a brighter title and `▸` marker;
the selected row is a brighter title, not an opaque rectangle. The header shows the
library and index freshness; the footer shows the `1/2/3` pane hotkeys, active
pane, messages and search/help/quit hints. Saved queries appear in the left
pane and are plain `queries/*.query` files.

Set `$EDITOR`, `$PDF_VIEWER`, or the `editor`/`viewer` keys in
`~/.config/papermgr/config.toml`.

## Search language

```
attention transformer              implicit AND, BM25 ranked
title:"attention is all you need"  phrase in a field
author:vaswani venue:neurips       field filters
year:2015..2020  added:2026-01..2026-11
tag:ml/llm                         the tag and all descendants
tag:ml/llm/                        exactly this level
NOT tag:status/skim   -tag:status/done
sort:year-  sort:added-  sort:title+  sort:relevance
```

Bare terms search title, abstract, keywords and extracted full text with
weights `title ×3`, `keywords ×2`, `abstract ×1.5`, `content ×1`. Fields:
`title abstract author affiliation venue year added updated tag keyword doi
arxiv citekey type content note web md`. Parse errors point at the offending
column and expected token.

## CLI

```
add <pdf>... [--yes] [--replace] [--tag t]...
search <query>          list
show <citekey>          meta show|set|refresh
tag list|add|rm|rename|merge|delete
attach <citekey> <file>... [--kind ...]   attach <citekey> --web <url>
note new <citekey> --format md|tex|typ
open <citekey> [--pdf|--note|--supplement NAME]
convert <citekey>       rekey [--dry-run] [--force]
rename <old> <new> [--dry-run] [--yes]   rm|restore|purge
reindex [--rebuild]     bib export [--dry-run]
query list|save|rm|run  plugin list|install|enable|disable
tui                     migrate
```

Every command accepts `--json` (where a structured result makes sense) and
`--library <dir>`. Exit codes: `0` success, `1` general error, `2` bad usage,
`3` library locked by another instance, `4` plugin failure.

## Data layout

```
library/
├── config.toml                 # citekey template, title words, plugins
├── library.bib                 # generated, one-way export
├── library.sqlite              # rebuildable FTS index
├── queries/unread-ml.query
├── .trash/20261110T143000-.../
├── .papermgr/lock
└── papers/vaswani-2017-attention/
    ├── paper.pdf
    ├── meta.json               # human-editable source of truth
    ├── attachments/
    │   ├── note/note.md        # at most one main note
    │   ├── supplement/
    │   ├── web/
    │   ├── markdown/           # convert plugin output
    │   └── misc/               # duplicate PDFs land here
    └── .papermgr/state.json    # provenance, locks, review, extraction
```

Hand-edit `meta.json` freely: the next reindex diffs it against the snapshot in
`state.json`, locks only the changed fields with `source: "manual"`, and
`meta refresh` will not overwrite them without `--force`.

`config.toml` example:

```toml
citekey_template = "{author}-{year}-{title}"
title_words = 3
index_supplements = false

[plugins.crossref.config]
mailto = "you@example.com"
```

## Plugins

Plugins are external processes speaking JSON-RPC 2.0 over stdio (one JSON object
per line). stdout carries protocol frames only; stderr is forwarded to the
structured log with the plugin name. Capabilities: `metadata`, `extract`,
`convert`, `capture`.

The bundled `plugins/crossref` resolves DOIs through Crossref and arXiv IDs via
the arXiv Atom API, with a conservative title-search fallback. Install it with:

```sh
papermgr plugin install ./plugins/crossref
papermgr plugin list
```

See [`docs/plugin-protocol.md`](docs/plugin-protocol.md) for the manifest,
methods, timeouts and a minimal shell plugin.

`convert` and `--web` capture deliberately have no built-in implementation: a
missing plugin is an explicit error, never a silent empty result.

## Correctness and tests

```sh
CGO_ENABLED=0 go test ./...
go vet ./...
```

Coverage includes:

- query parser table tests with syntax positions
- SQLite/FTS5 search, tag hierarchy, ranges, sorting, citekey rename
- store integration tests on real temporary directories covering INV-1…INV-9:
  citekey==directory, exactly one PDF, one note, meta/source-of-truth,
  state deletion, index rebuild, dry-run rekey + relative-link rewriting,
  `.trash` deletion/restore/purge, ghost-record removal
- manual `meta.json` edit detection and field locking
- plugin protocol tests: handshake, candidates, crash, timeout, version mismatch
- TUI view smoke tests for every panel

Design invariants, the full requirement contract and open questions live in
[`../需求分析.md`](../需求分析.md).

## Non-goals (v1)

No PDF annotation or built-in reader, no BibTeX import, no inbox watching, no
multi-instance writes, no plugin marketplace, no sync/server. See the
requirements document for the reasoning.
