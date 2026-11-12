# PaperManage (`papermgr`)

A tag-centric, PDF-first, single-user paper manager for the terminal. The TUI is
the main interface; the CLI is a first-class citizen. Every paper is a plain
directory, `meta.json` is the source of truth, and `library.sqlite` is a
throw-away search index.

Three panes: a tag outline on the left, the result list in the middle, and the
selected paper's metadata, tags and abstract on the right. The header shows the
library path, result count and index freshness; the footer shows the active
pane and key hints.

The live 150×44 capture of `~/Papers/papermgr` is in
[`docs/tui-screenshot.txt`](docs/tui-screenshot.txt).

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
- Python 3 for the bundled Crossref/arXiv and OpenAlex metadata plugins
  (optional)

## Build and install

```sh
make build          # -> ./bin/papermgr (embeds git describe as the version)
make test           # unit + integration tests
make install        # installs to $GOBIN, defaulting to ~/.local/bin
```

`make install` prints the version it embeds, e.g. `papermgr 41f25a2`. Use
`papermgr version` to confirm which checkout is on your `PATH`. Override the
destination with `make install GOBIN=/some/dir`.

Manual build:

```sh
CGO_ENABLED=0 go build -o bin/papermgr ./cmd/papermgr
```

## Quick start

```sh
# 1. Create a library (or `papermgr init ~/papers`)
papermgr init ~/papers

# 2. Install the bundled metadata plugins (optional but recommended)
papermgr plugin install ./plugins/openalex
papermgr plugin install ./plugins/crossref   # fallback for gaps OpenAlex leaves

# 3. Ingest PDFs; metadata plugins fill title/authors/DOI and set confidence
papermgr add ~/Downloads/*.pdf --tag ml/llm --tag status/to-read --yes

# 4. Browse, search, tag, open notes — all in the TUI
papermgr tui

# 5. Export for LaTeX
papermgr bib export
```

The default library comes from (in order) `--library`, `$PAPERMGR_LIBRARY`, or
`~/.config/papermgr/config.toml`. In this workspace it is set to
`~/Papers/papermgr`:

```toml
library = "/home/naqin/Papers/papermgr"
editor = "nvim"
viewer = "xdg-open"
```

## TUI

Three panes: tag tree, result list, detail. Everything is keyboard driven, with
optional mouse support.

| Key | Action |
|---|---|
| `/` | search prompt |
| `:` | command prompt (`:tag add ml/llm`, `:reindex --rebuild`, …) |
| `?` | full help |
| `1` `2` `3` | focus tag tree / result list / detail (btop-style) |
| `Tab` / `Shift+Tab` | cycle focus through the three panes |
| `h` `l` / `←` `→` | move focus; in the tag pane, collapse/expand the cursor tag |
| `z` | zoom the focused pane (again to restore) |
| `j k` `g G` | move / scroll the focused pane; first / last |
| `esc` | clear error, then marks, then filters; otherwise return to results |
| `space` | mark a paper; in the tag pane, select/deselect a tag |
| `-` / `\|` | in the tag pane: exclude a tag / toggle AND-OR for selected tags |
| `enter` | in the tag pane: apply selected tags (none selected = filter cursor tag) |
| `t` | add/remove tags on selection (`ml/llm -status/skim`) |
| `y` / `Y` | copy citekey / DOI |
| `U` / `ctrl+a` | clear marks / mark all visible papers |
| `x` | rename to the template-suggested citekey |
| `o` / `e` | open PDF / note in external programs |
| `c` | convert PDF to Markdown (convert plugin) |
| `d` | move to `.trash` (confirmation, never immediate deletion) |
| `p` | pending metadata review panel (per-field accept / keep) |
| `L` | structured log panel, including plugin stderr |
| `b` | background task panel |
| `R` | reindex changes detected by the stat-only scan |
| `r` | reload tags, results, index freshness and pending count |
| `s` | cycle sort: relevance, year↓, added↓, title↑, year↑ |
| `q` | quit |

Mouse: click a pane to focus it, click a tag or result row to select it, and use
the wheel to move inside the focused pane or the pane under the pointer.

The colour scheme is **Tokyo Night** (Night on dark terminals, Day on light
terminals), foreground-only: `#c0caf5` text, `#7aa2f7` accent, `#7dcfff`
tags/links and matching green/yellow/red semantics. No cell background is
painted, so Ghostty transparency and blur remain visible through the whole UI.
Colour is used as state, not decoration:

- result rows show a status glyph (`●` to-read, `◐` reading, `✓` read, `◦` skim)
  and a green `✎` when a note exists;
- tag names are coloured by namespace (`status/*` semantic colours, `method/*`
  accent, `application/*` cyan);
- selected, excluded and marked rows carry distinct glyphs, so state survives
  colour-blind terminals and `NO_COLOR`.

The tag pane uses a Logseq-style outline: `▾`/`▸` collapse parents, `•` marks
leaves, and `│` guides show ancestry. `h`/`l` (or `←`/`→`) collapse and expand
the cursor tag. `space` selects tags for a multi-tag filter; `enter` applies the
selection with the current operator (`|` switches AND/OR), and `-` excludes a
tag. Active filters are shown as chips in the second header line, including the
search expression and `NOT` terms. Changing a filter clears paper marks so bulk
actions can never touch hidden rows.

The layout is intentionally flat: no full-width colour bars, no rounded boxes,
no filled selection block. The active pane is a brighter title and `▸` marker;
the selected row is a brighter title, not an opaque rectangle. Below 72 columns
the UI collapses to the focused pane; `z` gives the same full-width view at any
size. The header shows the library, index freshness, result count and schema
issues; the footer shows the `1/2/3` pane hotkeys, focus-specific keys, the
marked count, messages and search/help/quit hints. Saved queries appear in the
left pane and are plain `queries/*.query` files.

Background work (index freshness and the pending-review count) runs
asynchronously; the 3-second synchronous filesystem scan is gone. Changes made
outside the TUI are picked up by `r` or the 15-second freshness check.

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
search <query>          list [query]
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

### Listing, searching and picking

`list` and `search` share one query grammar and one output layer:

```
papermgr list                                  # newest first
papermgr list tag:ml/llm year:2024..           # list accepts a query
papermgr list --sort title+ --limit 20
papermgr search "cloud removal" --sort year-
papermgr list --tsv | cut -f1                  # citekey per line
papermgr list --fzf                            # interactive picker
papermgr list --fzf --fzf-multi                # print several citekeys
papermgr open "$(papermgr list --fzf)"         # pick, then open
```

Output is terminal-aware:

- On a TTY the table shows status, year, title, citekey and tags, with the same
  status glyphs and semantic colours as the TUI.
- When stdout is not a TTY (or with `--tsv`) it emits stable, untruncated TSV:
  `citekey<TAB>year<TAB>status<TAB>title<TAB>tags`; the result count goes to
  stderr. `--no-tags` only affects the human table.
- `--limit N` caps rows and reports `showing N of M`; `--sort` accepts
  `relevance`, `year-`, `year+`, `added-`, `added+`, `title+`, `title-`.
- `--fzf` runs fzf with a `papermgr show` preview and prints the selected
  citekey(s) to stdout, ready for `xargs`/command substitution. The store is
  closed before fzf starts, so previews do not hit the library lock.
  `--no-preview` disables the preview pane.
- `--json` and `--tsv` win over a configured default fzf; explicitly combining
  `--fzf` with either is a usage error.

To make fzf the default for interactive `list`/`search`, add to
`~/.config/papermgr/config.toml`:

```toml
fzf = true
```

The default is only applied when stdout is a TTY; piping or redirecting output
still produces TSV. `--fzf=false` overrides the config per command.

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

[plugins.openalex.config]
mailto = "you@example.com"
# optional free key from https://openalex.org/settings/api;
# $OPENALEX_API_KEY is used when this is empty
# api_key = ""

[plugins.crossref.config]
mailto = "you@example.com"
```

## Plugins

Plugins are external processes speaking JSON-RPC 2.0 over stdio (one JSON object
per line). stdout carries protocol frames only; stderr is forwarded to the
structured log with the plugin name. Capabilities: `metadata`, `extract`,
`convert`, `capture`.

Two metadata plugins are bundled:

- `plugins/openalex` (priority 110) resolves DOIs and arXiv IDs through
  OpenAlex, with a title-search fallback. It contributes ORCIDs, affiliations,
  abstracts, keywords/topics, venue details and page ranges. An optional free
  API key raises the daily request budget and can be set in `config.toml` or
  `$OPENALEX_API_KEY`.
- `plugins/crossref` (priority 100) resolves DOIs through Crossref and arXiv IDs
  via the arXiv Atom API, with a conservative title-search fallback.

When both are installed, OpenAlex is applied first and Crossref fills fields it
left empty (metadata fields are only filled when missing). Install either or
both with:

```sh
papermgr plugin install ./plugins/openalex
papermgr plugin install ./plugins/crossref
papermgr plugin list
```

For papers already in the library, re-run metadata lookup with
`papermgr meta refresh <citekey>` (or `papermgr meta refresh --all --pending`).

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
