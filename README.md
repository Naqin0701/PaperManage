# PaperManage

The implementation lives in [`papermgr/`](papermgr/). It is a Go/Go-TUI app
built from [`需求分析.md`](需求分析.md): tag-only taxonomy, PDF-only ingest, and
plain files as the source of truth.

```sh
cd papermgr
make test
make build
papermgr tui
```

The working library lives at `~/Papers/papermgr` and is registered in
`~/.config/papermgr/config.toml`. See
[`papermgr/README.md`](papermgr/README.md) for the full guide.
