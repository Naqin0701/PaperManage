#!/bin/sh
# Configure a stable, ready-to-use demo using the default papermgr config.
# Usage: ./scripts/setup-demo.sh [--reset]
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
DEMO_ROOT=${DEMO_ROOT:-$HOME/papermgr-demo}
LIB="$DEMO_ROOT/library"
INBOX="$DEMO_ROOT/inbox"
P="$ROOT/bin/papermgr"
RESET=0
[ "${1:-}" = "--reset" ] && RESET=1

if [ ! -x "$P" ]; then
  echo "building papermgr…"
  (cd "$ROOT" && CGO_ENABLED=0 go build -o bin/papermgr ./cmd/papermgr)
fi

if [ -e "$DEMO_ROOT" ]; then
  if [ "$RESET" = "1" ]; then
    echo "removing old demo at $DEMO_ROOT"
    rm -rf "$DEMO_ROOT"
  else
    echo "demo already exists at $DEMO_ROOT"
    echo "run: ./scripts/setup-demo.sh --reset"
    exit 1
  fi
fi

CONFIG="$HOME/.config/papermgr/config.toml"
if [ -f "$CONFIG" ]; then
  BACKUP="$CONFIG.bak.$(date +%Y%m%dT%H%M%S)"
  cp "$CONFIG" "$BACKUP"
  echo "backed up previous config to $BACKUP"
fi

mkdir -p "$INBOX" "$DEMO_ROOT"

python3 - "$INBOX" <<'PY'
import os, sys

def pdf(path, title, doi, extra=""):
    lines = [f"BT /F1 16 Tf 72 730 Td ({title}) Tj ET"]
    if doi:
        lines.append(f"BT /F1 10 Tf 72 710 Td (doi:{doi}) Tj ET")
    if extra:
        lines.append(f"BT /F1 11 Tf 72 680 Td ({extra}) Tj ET")
    content = "\n".join(lines).encode()
    objs = [
        b"<< /Type /Catalog /Pages 2 0 R >>",
        b"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
        b"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
        b"<< /Length %d >>\nstream\n" % len(content) + content + b"\nendstream",
        b"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
    ]
    out = bytearray(b"%PDF-1.4\n"); offs = [0]
    for i, o in enumerate(objs, 1):
        offs.append(len(out)); out += b"%d 0 obj\n" % i + o + b"\nendobj\n"
    xref = len(out)
    out += b"xref\n0 %d\n0000000000 65535 f \n" % (len(objs) + 1)
    for o in offs[1:]:
        out += b"%010d 00000 n \n" % o
    out += b"trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n" % (len(objs) + 1, xref)
    open(path, "wb").write(out)

d = sys.argv[1]
pdf(os.path.join(d, "vaswani-2017-attention.pdf"), "Attention Is All You Need",
    "10.48550/arXiv.1706.03762", "The dominant sequence transduction models use attention.")
pdf(os.path.join(d, "lewis-2020-rag.pdf"), "Retrieval-Augmented Generation for Knowledge-Intensive NLP Tasks",
    "10.48550/arXiv.2005.11401", "We explore retrieval augmented generation.")
pdf(os.path.join(d, "hochreiter-1997-lstm.pdf"), "Long Short-Term Memory",
    "10.1162/neco.1997.9.8.1735", "A recurrent network architecture.")
pdf(os.path.join(d, "unpublished-notes.pdf"), "A Study of Nothing in Particular",
    "", "No identifier is available.")
PY

echo "initialising $LIB"
"$P" --library "$LIB" init "$LIB" >/dev/null

echo "installing crossref plugin…"
"$P" plugin install "$ROOT/plugins/crossref" >/dev/null 2>&1 || true

echo "adding sample papers…"
"$P" --library "$LIB" add "$INBOX/vaswani-2017-attention.pdf" --tag ml/llm/transformer --tag status/read --yes
"$P" --library "$LIB" add "$INBOX/lewis-2020-rag.pdf" --tag ml/llm/rag --tag status/reading --yes
"$P" --library "$LIB" add "$INBOX/hochreiter-1997-lstm.pdf" --tag ml/llm/rnn --tag status/read --yes
"$P" --library "$LIB" add "$INBOX/unpublished-notes.pdf" --tag status/skim --yes

CK=$("$P" --library "$LIB" list --json | python3 -c 'import json,sys; d=json.load(sys.stdin); print(next((x["citekey"] for x in d if "vaswani" in x["citekey"]), d[0]["citekey"]))')
NOTE=$("$P" --library "$LIB" note new "$CK" --format md)
cat >> "$NOTE" <<'MD'

## Why this paper

Demo note created by `scripts/setup-demo.sh`. Edit it in nvim with `e` from the
TUI, or from the shell with `papermgr open <citekey> --note`.
MD

echo "This is a demo supplement attachment." > "$DEMO_ROOT/appendix.txt"
"$P" --library "$LIB" attach "$CK" "$DEMO_ROOT/appendix.txt" --kind supplement >/dev/null
"$P" --library "$LIB" query save recent 'added:2026-01..2026-12 tag:ml' >/dev/null
"$P" --library "$LIB" reindex >/dev/null

mkdir -p "$HOME/.local/bin"
install -m 0755 "$P" "$HOME/.local/bin/papermgr"

cat <<MSG

Demo ready.

  library: $LIB
  config:  $HOME/.config/papermgr/config.toml
  binary:  $HOME/.local/bin/papermgr

Try:

  papermgr list
  papermgr search 'tag:ml/llm sort:year-'
  papermgr tui

The 4th paper has no DOI and is in "Pending review" so you can try the review
panel (press p in the TUI). Reset everything with:

  $ROOT/scripts/setup-demo.sh --reset
MSG
