#!/bin/sh
# Build a disposable demo library with three papers and a pending review entry.
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
DEMO_DIR=${DEMO_DIR:-/tmp/papermgr-demo}
export XDG_CONFIG_HOME="$DEMO_DIR/home/.config"
export XDG_STATE_HOME="$DEMO_DIR/home/.local/state"
export PAPERMGR_LIBRARY="$DEMO_DIR/library"

rm -rf "$DEMO_DIR"
mkdir -p "$DEMO_DIR/in"

if [ ! -x "$ROOT/bin/papermgr" ]; then
  echo "building papermgr…"
  (cd "$ROOT" && CGO_ENABLED=0 go build -o bin/papermgr ./cmd/papermgr)
fi
P="$ROOT/bin/papermgr"

python3 - "$DEMO_DIR/in" <<'PY'
import os, sys, zlib

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
pdf(os.path.join(d, "vaswani-2017-attention.pdf"), "Attention Is All You Need", "10.48550/arXiv.1706.03762", "The dominant sequence transduction models use attention.")
pdf(os.path.join(d, "lewis-2020-rag.pdf"), "Retrieval-Augmented Generation for Knowledge-Intensive NLP Tasks", "10.48550/arXiv.2005.11401", "We explore retrieval augmented generation.")
pdf(os.path.join(d, "hochreiter-1997-lstm.pdf"), "Long Short-Term Memory", "10.1162/neco.1997.9.8.1735", "A recurrent network architecture.")
pdf(os.path.join(d, "unpublished-notes.pdf"), "A Study of Nothing in Particular", "", "No identifier is available.")
PY

"$P" init "$PAPERMGR_LIBRARY" >/dev/null
"$P" plugin install "$ROOT/plugins/crossref" >/dev/null 2>&1 || true
"$P" add "$DEMO_DIR/in/vaswani-2017-attention.pdf" --tag ml/llm/transformer --tag status/read --yes
"$P" add "$DEMO_DIR/in/lewis-2020-rag.pdf" --tag ml/llm/rag --tag status/reading --yes
"$P" add "$DEMO_DIR/in/hochreiter-1997-lstm.pdf" --tag ml/llm/rnn --tag status/read --yes
"$P" add "$DEMO_DIR/in/unpublished-notes.pdf" --tag status/skim --yes
CK=$("$P" list --json | python3 -c 'import json,sys; d=json.load(sys.stdin); print(next(x["citekey"] for x in d if "vaswani" in x["citekey"]))' 2>/dev/null || true)
[ -n "${CK:-}" ] && "$P" note new "$CK" --format md >/dev/null 2>&1 || true
"$P" query save recent 'added:2026-01..2026-12 tag:ml' >/dev/null

cat <<MSG
demo library: $PAPERMGR_LIBRARY
demo config:  $XDG_CONFIG_HOME

  export XDG_CONFIG_HOME=$XDG_CONFIG_HOME
  export XDG_STATE_HOME=$XDG_STATE_HOME
  export PAPERMGR_LIBRARY=$PAPERMGR_LIBRARY
  $P tui

  $P search 'tag:ml/llm sort:year-'
  $P show <citekey>
MSG
