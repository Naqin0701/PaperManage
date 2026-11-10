package store_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"papermgr/internal/config"
	"papermgr/internal/model"
	"papermgr/internal/store"
)

func TestMain(m *testing.M) {
	dir, _ := os.MkdirTemp("", "papermgr-config-*")
	os.Setenv("XDG_CONFIG_HOME", dir)
	os.Setenv("XDG_STATE_HOME", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func newStore(t *testing.T) *store.Store {
	t.Helper()
	root := filepath.Join(t.TempDir(), "library")
	g := config.Global{Editor: "true", Viewer: "true", LogDir: filepath.Join(t.TempDir(), "log")}
	s, err := store.Init(root, g)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func writePDF(t *testing.T, path, title, doi string) {
	t.Helper()
	content := fmt.Sprintf("BT /F1 14 Tf 72 720 Td (%s) Tj ET\nBT /F1 10 Tf 72 700 Td (doi:%s) Tj ET",
		pdfEscape(title), pdfEscape(doi))
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	var b strings.Builder
	b.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objs)+1)
	for i, obj := range objs {
		offsets[i+1] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for i := 1; i <= len(objs); i++ {
		fmt.Fprintf(&b, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func pdfEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "(", `\(`)
	s = strings.ReplaceAll(s, ")", `\)`)
	return s
}

func add(t *testing.T, s *store.Store, path, title, doi string, tags ...string) store.AddResult {
	t.Helper()
	writePDF(t, path, title, doi)
	res, err := s.AddPDF(path, store.AddOptions{Tags: tags, Yes: true})
	if err != nil {
		t.Fatalf("AddPDF: %v", err)
	}
	return res
}

func TestAddInvariants(t *testing.T) {
	s := newStore(t)
	pdf := filepath.Join(t.TempDir(), "vaswani-2017-attention.pdf")
	res := add(t, s, pdf, "Attention Is All You Need", "10.48550/arXiv.1706.03762", "ml/llm/transformer", "status/reading")
	if res.Citekey == "" {
		t.Fatal("empty citekey")
	}
	p, err := s.Load(res.Citekey)
	if err != nil {
		t.Fatal(err)
	}
	// INV-1
	if p.Meta.Citekey != res.Citekey || filepath.Base(p.Dir) != res.Citekey {
		t.Errorf("INV-1 violated: meta=%q dir=%q", p.Meta.Citekey, p.Dir)
	}
	// INV-2
	if _, err := os.Stat(filepath.Join(p.Dir, "paper.pdf")); err != nil {
		t.Errorf("INV-2 violated: %v", err)
	}
	// meta.json human-readable
	data, _ := os.ReadFile(filepath.Join(p.Dir, "meta.json"))
	if !strings.Contains(string(data), `"schema_version": 1`) || !strings.Contains(string(data), `"id": "`) {
		t.Errorf("meta.json not as expected: %s", string(data))
	}
	// INV-4: state.json separate
	if _, err := os.Stat(filepath.Join(p.Dir, ".papermgr", "state.json")); err != nil {
		t.Errorf("state.json missing: %v", err)
	}
	if p.Meta.PDF.SHA256 == "" || p.Meta.PDF.OriginalName == "" {
		t.Error("pdf hash/original name missing")
	}
	if p.Meta.PDF.Pages == 0 {
		t.Error("pdf page count not populated")
	}
	if p.State.Index.ContentSHA256 == "" || p.State.Index.ExtractedAt == "" {
		t.Errorf("extraction state missing: %+v", p.State.Index)
	}
	if p.State.Review.Status != "pending" {
		t.Errorf("review status = %q, want pending", p.State.Review.Status)
	}
	if _, err := os.Stat(filepath.Join(s.Root, "library.bib")); err != nil {
		t.Errorf("library.bib not generated: %v", err)
	}
	// Meta is sole truth for tags: parent tags are not persisted.
	for _, tag := range p.Meta.Tags {
		if tag == "ml" || tag == "ml/llm" {
			t.Errorf("implicit parent tag persisted: %q", tag)
		}
	}
	hits, err := s.Index.Query("tag:ml/llm")
	if err != nil || len(hits) != 1 {
		t.Errorf("tag search hits=%v err=%v", hits, err)
	}
	if hits[0].ID != p.Meta.ID {
		t.Errorf("index ID mismatch")
	}
}

func TestDuplicateSHAAndDOI(t *testing.T) {
	s := newStore(t)
	dir := t.TempDir()
	first := add(t, s, filepath.Join(dir, "paper-one.pdf"), "Attention Is All You Need", "10.1000/dupe")
	secondPath := filepath.Join(dir, "copy.pdf")
	writePDF(t, secondPath, "Attention Is All You Need", "10.1000/dupe")
	res, err := s.AddPDF(secondPath, store.AddOptions{Yes: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Duplicate || res.Citekey != first.Citekey {
		t.Fatalf("expected duplicate attached to %s, got %+v", first.Citekey, res)
	}
	keys, _ := s.AllCitekeys()
	if len(keys) != 1 {
		t.Errorf("duplicate created new entries: %v", keys)
	}
	att, _ := s.Attachments(first.Citekey)
	found := false
	for _, a := range att {
		if a.Kind == "misc" && strings.HasPrefix(filepath.Base(a.Rel), "duplicate-") {
			found = true
		}
	}
	if !found {
		t.Errorf("duplicate PDF not archived in misc: %+v", att)
	}
}

func TestTagRenameCascadeAndDryRun(t *testing.T) {
	s := newStore(t)
	dir := t.TempDir()
	a := add(t, s, filepath.Join(dir, "a.pdf"), "Paper A", "10.1/a", "ml/llm/rag", "status/reading")
	_ = add(t, s, filepath.Join(dir, "b.pdf"), "Paper B", "10.1/b", "ml/llm")
	changes, err := s.RenameTag("ml/llm", "ml/transformers", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 2 {
		t.Errorf("dry run changes = %+v", changes)
	}
	p, _ := s.Load(a.Citekey)
	if !contains(p.Meta.Tags, "ml/llm/rag") {
		t.Fatal("dry run modified metadata")
	}
	if _, err := s.RenameTag("ml/llm", "ml/transformers", false); err != nil {
		t.Fatal(err)
	}
	p, _ = s.Load(a.Citekey)
	if !contains(p.Meta.Tags, "ml/transformers/rag") || contains(p.Meta.Tags, "ml/llm/rag") {
		t.Errorf("cascade failed: %v", p.Meta.Tags)
	}
	tags, _ := s.Tags()
	count := map[string]int{}
	for _, tc := range tags {
		count[tc.Tag] = tc.Count
	}
	if count["ml"] != 2 || count["ml/transformers"] != 2 {
		t.Errorf("counts = %v", count)
	}
	// tag merge
	if _, err := s.MergeTags("status/reading", "status/read", false); err != nil {
		t.Fatal(err)
	}
	p, _ = s.Load(a.Citekey)
	if !contains(p.Meta.Tags, "status/read") {
		t.Errorf("merge failed: %v", p.Meta.Tags)
	}
	// delete only unbinds
	if _, err := s.DeleteTag("status/read", false); err != nil {
		t.Fatal(err)
	}
	p, _ = s.Load(a.Citekey)
	if contains(p.Meta.Tags, "status/read") {
		t.Errorf("delete did not unbind: %v", p.Meta.Tags)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "paper.pdf")); err != nil {
		t.Errorf("delete tag removed paper: %v", err)
	}
}

func TestRebuildRecoversIndex(t *testing.T) {
	s := newStore(t)
	dir := t.TempDir()
	add(t, s, filepath.Join(dir, "one.pdf"), "Attention Is All You Need", "10.1/one")
	add(t, s, filepath.Join(dir, "two.pdf"), "Retrieval Augmented Generation", "10.1/two")
	before, err := s.Index.Query("attention OR retrieval")
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 2 {
		t.Fatalf("before hits = %d", len(before))
	}
	root := s.Root
	logDir := s.Global.LogDir
	editor, viewer := s.Global.Editor, s.Global.Viewer
	s.Close()
	for _, f := range []string{"library.sqlite", "library.sqlite-wal", "library.sqlite-shm"} {
		os.Remove(filepath.Join(root, f))
	}
	s2, err := store.Open(root, config.Global{Editor: editor, Viewer: viewer, LogDir: logDir})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if err := s2.EnsureIndex(nil); err != nil {
		t.Fatal(err)
	}
	after, err := s2.Index.Query("attention OR retrieval")
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Errorf("after rebuild hits = %d, want %d", len(after), len(before))
	}
	// INV-6: content search still works.
	hits, err := s2.Index.Query("content:attention")
	if err != nil || len(hits) != 1 {
		t.Errorf("content search after rebuild: %v %v", hits, err)
	}
}

func TestRekeyAndLinkRewrite(t *testing.T) {
	s := newStore(t)
	dir := t.TempDir()
	a := add(t, s, filepath.Join(dir, "a.pdf"), "First Paper", "10.1/a", "ml/llm")
	b := add(t, s, filepath.Join(dir, "b.pdf"), "Second Paper", "10.1/b")
	if err := s.MetaSet(a.Citekey, "title", "A Completely New Title"); err != nil {
		t.Fatal(err)
	}
	note, err := s.NoteNew(a.Citekey, "md", false)
	if err != nil {
		t.Fatal(err)
	}
	ref := "../" + b.Citekey + "/paper.pdf"
	if err := os.WriteFile(note, []byte("# note\n\nSee ["+b.Citekey+"]("+ref+")\n[[linked ]]"), 0o644); err != nil {
		t.Fatal(err)
	}
	ops, err := s.PlanRekey(false)
	if err != nil {
		t.Fatal(err)
	}
	var opA store.RenameOp
	for _, op := range ops {
		if op.Old == a.Citekey {
			opA = op
		}
	}
	if opA.New == "" || opA.New == opA.Old {
		t.Fatalf("expected rekey for %s, ops=%+v", a.Citekey, ops)
	}
	if _, err := os.Stat(s.PaperDir(opA.New)); err == nil {
		t.Fatal("dry run renamed a directory")
	}
	applied, err := s.ApplyRekey(ops)
	if err != nil {
		t.Fatal(err)
	}
	newB := b.Citekey
	for _, op := range applied {
		if op.Old == b.Citekey {
			newB = op.New
		}
	}
	p, err := s.Load(opA.New)
	if err != nil {
		t.Fatal(err)
	}
	if p.Meta.Citekey != opA.New {
		t.Errorf("meta citekey not updated")
	}
	if _, err := s.Load(a.Citekey); err == nil {
		t.Errorf("old citekey still loads")
	}
	// Rename B and verify A's note reference is rewritten.
	rop, err := s.RenamePaper(newB, "renamed-second-paper")
	if err != nil {
		t.Fatal(err)
	}
	if len(rop.Wikilinks) == 0 {
		t.Errorf("wikilink not reported: %+v", rop)
	}
	noteData, _ := os.ReadFile(filepath.Join(s.PaperDir(opA.New), "attachments", "note", "note.md"))
	if !strings.Contains(string(noteData), "renamed-second-paper/paper.pdf") {
		t.Errorf("relative link not rewritten:\n%s", noteData)
	}
	if !strings.Contains(string(noteData), "[[linked ]]") {
		t.Errorf("wikilink was modified:\n%s", noteData)
	}
	// INV-1 after rekey
	keys, _ := s.AllCitekeys()
	for _, k := range keys {
		pp, err := s.Load(k)
		if err != nil {
			t.Fatalf("load %s: %v", k, err)
		}
		if pp.Meta.Citekey != k {
			t.Errorf("INV-1: dir %q has meta citekey %q", k, pp.Meta.Citekey)
		}
	}
}

func TestTrashRestorePurge(t *testing.T) {
	s := newStore(t)
	dir := t.TempDir()
	res := add(t, s, filepath.Join(dir, "a.pdf"), "Paper", "10.1/a")
	trashPath, err := s.Rm(res.Citekey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(trashPath); err != nil {
		t.Errorf("INV-8: trash entry missing")
	}
	if _, err := os.Stat(s.PaperDir(res.Citekey)); err == nil {
		t.Errorf("paper still in papers/")
	}
	if hits, err := s.Index.Query(""); err != nil || len(hits) != 0 {
		t.Errorf("index still has hits: %v %v", hits, err)
	}
	entries, _ := s.ListTrash()
	if len(entries) != 1 {
		t.Fatalf("trash entries = %d", len(entries))
	}
	if _, err := s.Restore(entries[0].Name); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(res.Citekey); err != nil {
		t.Errorf("restored paper not loadable: %v", err)
	}
	if _, err := s.Rm(res.Citekey); err != nil {
		t.Fatal(err)
	}
	n, err := s.Purge(0)
	if err != nil || n != 1 {
		t.Errorf("purge = %d, %v", n, err)
	}
}

func TestBibExportAndNoteUniqueness(t *testing.T) {
	s := newStore(t)
	dir := t.TempDir()
	res := add(t, s, filepath.Join(dir, "a.pdf"), "Attention Is All You Need", "10.1/a")
	if _, err := s.NoteNew(res.Citekey, "md", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.NoteNew(res.Citekey, "tex", false); err == nil {
		t.Error("INV-3: second note allowed")
	}
	bib, err := os.ReadFile(filepath.Join(s.Root, "library.bib"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(bib), "generated by papermgr") || !strings.Contains(string(bib), "do not edit") {
		t.Errorf("bib header missing:\n%s", bib)
	}
	if !strings.Contains(string(bib), "@article{"+res.Citekey) {
		t.Errorf("bib entry missing:\n%s", bib)
	}
}

func TestMetaSetLocksField(t *testing.T) {
	s := newStore(t)
	dir := t.TempDir()
	res := add(t, s, filepath.Join(dir, "a.pdf"), "Paper", "10.1/a")
	if err := s.MetaSet(res.Citekey, "year", "2024"); err != nil {
		t.Fatal(err)
	}
	p, _ := s.Load(res.Citekey)
	if p.Meta.Year == nil || *p.Meta.Year != 2024 {
		t.Errorf("year = %v", p.Meta.Year)
	}
	if !p.State.Fields["year"].Locked || p.State.Fields["year"].Source != "manual" {
		t.Errorf("field not locked: %+v", p.State.Fields["year"])
	}
	// INV-5: deleting state.json loses only state.
	os.Remove(filepath.Join(p.Dir, ".papermgr", "state.json"))
	p2, err := s.Load(res.Citekey)
	if err != nil {
		t.Fatal(err)
	}
	if p2.Meta.Year == nil || *p2.Meta.Year != 2024 {
		t.Errorf("metadata lost with state.json")
	}
	if p2.State.Fields["year"].Locked {
		t.Errorf("state not reset")
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestDirtyCount(t *testing.T) {
	s := newStore(t)
	dir := t.TempDir()
	res := add(t, s, filepath.Join(dir, "a.pdf"), "Paper", "10.1/a")
	n, err := s.DirtyCount()
	if err != nil || n != 0 {
		t.Fatalf("dirty=%d err=%v", n, err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := s.MetaSet(res.Citekey, "title", "Changed"); err != nil {
		t.Fatal(err)
	}
	// MetaSet reindexes, so it should be clean again.
	n, _ = s.DirtyCount()
	if n != 0 {
		t.Errorf("dirty after reindex = %d", n)
	}
}

func TestManualEditLocksChangedFields(t *testing.T) {
	s := newStore(t)
	dir := t.TempDir()
	res := add(t, s, filepath.Join(dir, "a.pdf"), "Original", "10.1/a")
	p, err := s.Load(res.Citekey)
	if err != nil {
		t.Fatal(err)
	}
	metaPath := filepath.Join(p.Dir, "meta.json")
	data, _ := os.ReadFile(metaPath)
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	m["title"] = "Hand Edited Title"
	m["doi"] = "10.9999/hand"
	out, _ := json.MarshalIndent(m, "", "  ")
	os.WriteFile(metaPath, append(out, '\n'), 0o644)

	if err := s.SyncPaper(res.Citekey); err != nil {
		t.Fatal(err)
	}
	p, _ = s.Load(res.Citekey)
	if p.State.Fields["title"].Source != "manual" || !p.State.Fields["title"].Locked {
		t.Errorf("title not locked after manual edit: %+v", p.State.Fields["title"])
	}
	if p.State.Fields["doi"].Source != "manual" || !p.State.Fields["doi"].Locked {
		t.Errorf("doi not locked after manual edit: %+v", p.State.Fields["doi"])
	}
	if p.State.Fields["year"].Locked {
		t.Errorf("unrelated field unexpectedly locked: %+v", p.State.Fields["year"])
	}
	if p.Meta.Title != "Hand Edited Title" {
		t.Errorf("meta title = %q", p.Meta.Title)
	}
}

func TestSavedQueries(t *testing.T) {
	s := newStore(t)
	if err := s.SaveQuery("unread-ml", `tag:status/unread AND tag:ml`); err != nil {
		t.Fatal(err)
	}
	qs, err := s.ListQueries()
	if err != nil || len(qs) != 1 || qs[0].Name != "unread-ml" {
		t.Fatalf("queries = %+v err=%v", qs, err)
	}
	if _, err := os.Stat(filepath.Join(s.Root, "queries", "unread-ml.query")); err != nil {
		t.Fatal("query file missing")
	}
	expr, err := s.LoadQuery("unread-ml")
	if err != nil || expr == "" {
		t.Fatalf("load = %q err=%v", expr, err)
	}
	if err := s.DeleteQuery("unread-ml"); err != nil {
		t.Fatal(err)
	}
	if qs, _ := s.ListQueries(); len(qs) != 0 {
		t.Errorf("query not deleted")
	}
	if err := s.SaveQuery("../evil", "x"); err == nil {
		t.Error("path traversal accepted")
	}
}

func TestLockPreventsSecondInstance(t *testing.T) {
	s := newStore(t)
	root := s.Root
	_, err := store.Open(root, config.Global{Editor: "true", Viewer: "true", LogDir: t.TempDir()})
	if err == nil {
		t.Fatal("second Open succeeded; flock not enforced")
	}
	if !errors.Is(err, store.ErrLocked) {
		t.Fatalf("error = %v, want ErrLocked", err)
	}
}

func TestRekeyNoopDoesNotRename(t *testing.T) {
	s := newStore(t)
	dir := t.TempDir()
	res := add(t, s, filepath.Join(dir, "2020-some-paper.pdf"), "Some Paper", "10.1/noop", "ml/llm")
	ops, err := s.PlanRekey(false)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range ops {
		if op.Old == res.Citekey && !op.Skipped {
			t.Fatalf("stable citekey scheduled for rename: %+v", op)
		}
	}
	if _, err := s.ApplyRekey(ops); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(res.Citekey); err != nil {
		t.Fatalf("stable citekey lost: %v", err)
	}
}

func TestRekeyDryRunReportsLinks(t *testing.T) {
	s := newStore(t)
	dir := t.TempDir()
	a := add(t, s, filepath.Join(dir, "a.pdf"), "Paper A", "10.1/a")
	b := add(t, s, filepath.Join(dir, "b.pdf"), "Paper B", "10.1/b")
	if err := s.MetaSet(a.Citekey, "title", "Completely Different Work"); err != nil {
		t.Fatal(err)
	}
	note, _ := s.NoteNew(a.Citekey, "md", false)
	os.WriteFile(note, []byte("See ../"+b.Citekey+"/paper.pdf and [[wiki]]"), 0o644)
	ops, err := s.PlanRekey(false)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range ops {
		if op.Old == a.Citekey {
			if len(op.Wikilinks) == 0 {
				t.Errorf("dry-run did not report wikilink: %+v", op)
			}
		}
	}
}

func TestTagCountsUniquePapers(t *testing.T) {
	s := newStore(t)
	dir := t.TempDir()
	res := add(t, s, filepath.Join(dir, "a.pdf"), "Paper", "10.1/a", "ml", "ml/llm")
	tags, err := s.Tags()
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]model.TagCount{}
	for _, tc := range tags {
		counts[tc.Tag] = tc
	}
	if counts["ml"].Count != 1 {
		t.Errorf("ml count = %d, want 1 unique paper", counts["ml"].Count)
	}
	if counts["ml/llm"].Count != 1 || counts["ml/llm"].Explicit != 1 {
		t.Errorf("ml/llm = %+v", counts["ml/llm"])
	}
	if err := s.RemoveTags(res.Citekey, "ml"); err != nil {
		t.Fatal(err)
	}
}

func TestReindexRemovesGhosts(t *testing.T) {
	s := newStore(t)
	dir := t.TempDir()
	res := add(t, s, filepath.Join(dir, "a.pdf"), "Paper", "10.1/a")
	if err := os.RemoveAll(s.PaperDir(res.Citekey)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reindex(false, nil); err != nil {
		t.Fatal(err)
	}
	hits, err := s.Index.Query("")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Errorf("INV-9: ghost records remain: %+v", hits)
	}
}

func TestAddDuplicateWithTags(t *testing.T) {
	s := newStore(t)
	dir := t.TempDir()
	first := add(t, s, filepath.Join(dir, "a.pdf"), "Paper", "10.1/a")
	res, err := s.AddPDF(filepath.Join(dir, "a.pdf"), store.AddOptions{Tags: []string{"ml/new"}, Yes: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Duplicate {
		t.Fatal("expected duplicate")
	}
	p, _ := s.Load(first.Citekey)
	if !contains(p.Meta.Tags, "ml/new") {
		t.Errorf("tags from duplicate add not attached: %v", p.Meta.Tags)
	}
}

func TestEnsureIndexAfterPartialWrite(t *testing.T) {
	s := newStore(t)
	dir := t.TempDir()
	add(t, s, filepath.Join(dir, "a.pdf"), "Paper A", "10.1/a")
	add(t, s, filepath.Join(dir, "b.pdf"), "Paper B", "10.1/b")
	root := s.Root
	logDir := s.Global.LogDir
	s.Close()
	for _, f := range []string{"library.sqlite", "library.sqlite-wal", "library.sqlite-shm"} {
		os.Remove(filepath.Join(root, f))
	}
	s2, err := store.Open(root, config.Global{Editor: "true", Viewer: "true", LogDir: logDir})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	// A write creates a partial index with only the new paper.
	add(t, s2, filepath.Join(dir, "c.pdf"), "Paper C", "10.1/c")
	if err := s2.EnsureIndex(nil); err != nil {
		t.Fatal(err)
	}
	hits, err := s2.Index.Query("")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 3 {
		t.Errorf("EnsureIndex after partial write: %d hits, want 3", len(hits))
	}
}

func TestInitUpdatesGlobalLibrary(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	root := filepath.Join(dir, "newlib")
	s, err := store.Init(root, config.Global{Editor: "true", Viewer: "true"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	g, err := config.LoadGlobal()
	if err != nil {
		t.Fatal(err)
	}
	abs, _ := filepath.Abs(root)
	if g.Library != abs {
		t.Errorf("global library = %q, want %q", g.Library, abs)
	}
}
