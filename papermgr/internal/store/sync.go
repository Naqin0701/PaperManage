package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"papermgr/internal/index"
	"papermgr/internal/model"
	"papermgr/internal/pdftext"
)

// SyncStats summarises an index synchronisation.
type SyncStats struct {
	Total     int           `json:"total"`
	Synced    int           `json:"synced"`
	Unchanged int           `json:"unchanged"`
	Removed   int           `json:"removed"`
	Errors    []string      `json:"errors,omitempty"`
	Elapsed   time.Duration `json:"elapsed"`
}

// EnsureIndex builds the index when the database is new or empty.
func (s *Store) EnsureIndex(progress func(done, total int)) error {
	dirs, err := s.AllCitekeys()
	if err != nil {
		return err
	}
	// A missing database may have been partially populated by a write
	// operation (e.g. add) before this query. Compare row count to dirs.
	if s.Index.Count() != len(dirs) {
		_, err = s.SyncAll(false, progress)
		return err
	}
	return nil
}

// SyncAll walks the library and updates changed index records.
func (s *Store) SyncAll(force bool, progress func(done, total int)) (SyncStats, error) {
	start := time.Now()
	stats := SyncStats{}
	dirs, err := s.AllCitekeys()
	if err != nil {
		return stats, err
	}
	stats.Total = len(dirs)
	stored, err := s.Index.Signatures()
	if err != nil {
		return stats, err
	}
	seen := map[string]bool{}
	for i, ck := range dirs {
		seen[ck] = true
		sig, err := s.paperSignature(ck)
		if err != nil {
			stats.Errors = append(stats.Errors, fmt.Sprintf("%s: %v", ck, err))
			continue
		}
		if !force && stored[ck] == sig && sig != "" {
			stats.Unchanged++
		} else {
			if err := s.SyncPaper(ck); err != nil {
				stats.Errors = append(stats.Errors, fmt.Sprintf("%s: %v", ck, err))
				s.Log.Error("index.sync_failed", map[string]interface{}{"citekey": ck, "error": err.Error()})
			} else {
				stats.Synced++
			}
		}
		if progress != nil {
			progress(i+1, len(dirs))
		}
	}
	for ck := range stored {
		if !seen[ck] {
			if err := s.Index.DeleteByCitekey(ck); err != nil {
				stats.Errors = append(stats.Errors, fmt.Sprintf("remove %s: %v", ck, err))
			} else {
				stats.Removed++
			}
		}
	}
	stats.Elapsed = time.Since(start)
	return stats, nil
}

// SyncPaper reindexes one paper from disk (full extraction).
func (s *Store) SyncPaper(citekey string) error {
	p, err := s.Load(citekey)
	if err != nil {
		return err
	}
	if p.Meta.Citekey != citekey {
		p.Meta.Citekey = citekey
		if err := s.SaveMeta(p); err != nil {
			return err
		}
		s.Log.Warn("paper.citekey_repaired", map[string]interface{}{"directory": citekey})
	}
	s.detectManualEdits(p)
	rec, idxState, err := s.buildRecord(p)
	if err != nil {
		return err
	}
	if rec.MetaChanged {
		if err := s.SaveMeta(p); err != nil {
			return err
		}
	}
	if sig, err := s.paperSignature(citekey); err == nil {
		rec.Sig = sig
	}
	if err := s.Index.Upsert(rec); err != nil {
		return err
	}
	p.State.Index = idxState
	if err := s.SaveState(p); err != nil {
		return err
	}
	return nil
}

// Reindex performs an incremental or full rebuild.
func (s *Store) Reindex(rebuild bool, progress func(done, total int)) (SyncStats, error) {
	if rebuild {
		if err := s.Index.Rebuild(); err != nil {
			return SyncStats{}, err
		}
	}
	stats, err := s.SyncAll(rebuild, progress)
	if err != nil {
		return stats, err
	}
	_ = s.Index.SetMeta("last_reindex", time.Now().Format(time.RFC3339))
	s.Log.Info("index.reindex", map[string]interface{}{
		"rebuild": rebuild, "synced": stats.Synced, "removed": stats.Removed,
		"unchanged": stats.Unchanged, "errors": len(stats.Errors), "elapsed_ms": stats.Elapsed.Milliseconds(),
	})
	return stats, nil
}

// DirtyCount is a stat-only freshness check (FR-7.3). It never parses PDFs.
func (s *Store) DirtyCount() (int, error) {
	dirs, err := s.AllCitekeys()
	if err != nil {
		return 0, err
	}
	stored, err := s.Index.Signatures()
	if err != nil {
		return 0, err
	}
	dirty := 0
	seen := map[string]bool{}
	for _, ck := range dirs {
		seen[ck] = true
		sig, err := s.paperSignature(ck)
		if err != nil || stored[ck] != sig {
			dirty++
		}
	}
	for ck := range stored {
		if !seen[ck] {
			dirty++
		}
	}
	return dirty, nil
}

// paperSignature hashes path+mtime+size of every file that can affect the index.
func (s *Store) paperSignature(citekey string) (string, error) {
	dir := s.PaperDir(citekey)
	var entries []string
	add := func(path string) {
		st, err := os.Stat(path)
		if err != nil {
			return
		}
		rel, _ := filepath.Rel(dir, path)
		entries = append(entries, fmt.Sprintf("%s:%d:%d", rel, st.ModTime().UnixNano(), st.Size()))
	}
	add(filepath.Join(dir, "meta.json"))
	add(filepath.Join(dir, "paper.pdf"))
	walkFiles(filepath.Join(dir, "attachments", "note"), add)
	walkFiles(filepath.Join(dir, "attachments", "markdown"), add)
	walkFiles(filepath.Join(dir, "attachments", "web"), add)
	if s.Cfg.IndexSupplements {
		walkFiles(filepath.Join(dir, "attachments", "supplement"), add)
	}
	entries = append(entries, fmt.Sprintf("supplements:%v", s.Cfg.IndexSupplements))
	sort.Strings(entries)
	h := sha256.Sum256([]byte(strings.Join(entries, "\n")))
	return hex.EncodeToString(h[:]), nil
}

func walkFiles(root string, fn func(string)) {
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		fn(path)
		return nil
	})
}

// buildRecord extracts all searchable text and assembles an index record.
func (s *Store) buildRecord(p *model.Paper) (index.Record, model.IndexState, error) {
	rec := index.Record{
		ID:           p.Meta.ID,
		Citekey:      p.Meta.Citekey,
		EntryType:    p.Meta.EntryType,
		Title:        p.Meta.Title,
		Abstract:     p.Meta.Abstract,
		Keywords:     strings.Join(p.Meta.Keywords, ", "),
		Authors:      authorsText(p.Meta.Authors),
		Affiliations: affiliationsText(p.Meta.Authors),
		Venue:        venueText(p.Meta.Venue),
		Year:         p.Meta.Year,
		DOI:          p.Meta.DOI,
		ArxivID:      p.Meta.ArxivID,
		URL:          p.Meta.URL,
		AddedAt:      p.Meta.AddedAt,
		UpdatedAt:    p.Meta.UpdatedAt,
		Tags:         p.Meta.Tags,
	}
	idx := model.IndexState{}
	var errs []string

	pdfPath := filepath.Join(p.Dir, "paper.pdf")
	if _, err := os.Stat(pdfPath); err != nil {
		errs = append(errs, "paper.pdf missing")
	} else {
		text, pages, ok := s.Plugins.ExtractText(pdfPath)
		var err error
		if !ok {
			text, pages, err = pdftext.Extract(pdfPath)
		}
		if err != nil {
			idx.Error = err.Error()
			errs = append(errs, err.Error())
		} else {
			rec.Content = text
			rec.Pages = pages
			sum := sha256.Sum256([]byte(text))
			idx.ContentSHA256 = hex.EncodeToString(sum[:])
			idx.ExtractedAt = time.Now().UTC().Format(time.RFC3339)
			if p.Meta.PDF.Pages == 0 && pages > 0 {
				p.Meta.PDF.Pages = pages
				rec.MetaChanged = true
			}
		}
	}

	noteDir := filepath.Join(p.Dir, "attachments", "note")
	var notes []string
	walkFiles(noteDir, func(path string) {
		switch strings.ToLower(filepath.Ext(path)) {
		case ".md", ".tex", ".typ":
			notes = append(notes, path)
		}
	})
	sort.Strings(notes)
	if len(notes) > 1 {
		msg := "multiple note files violate INV-3; note field not indexed"
		rec.IndexError = msg
		errs = append(errs, msg)
	} else if len(notes) == 1 {
		if data, err := os.ReadFile(notes[0]); err == nil {
			rec.Note = string(data)
			rec.HasNote = true
		}
	}

	rec.MD = readTextTree(filepath.Join(p.Dir, "attachments", "markdown"), []string{".md", ".markdown", ".txt"})
	rec.Web = readWebTree(filepath.Join(p.Dir, "attachments", "web"))
	if s.Cfg.IndexSupplements {
		rec.Content += "\n" + readTextTree(filepath.Join(p.Dir, "attachments", "supplement"), []string{".md", ".tex", ".typ", ".txt"})
	}
	if len(errs) > 0 {
		rec.IndexError = strings.Join(errs, "; ")
		if idx.Error == "" {
			idx.Error = rec.IndexError
		}
	}
	idx.MDSHA256 = nil
	if rec.MD != "" {
		sum := sha256.Sum256([]byte(rec.MD))
		h := hex.EncodeToString(sum[:])
		idx.MDSHA256 = &h
	}
	return rec, idx, nil
}

func authorsText(as []model.Author) string {
	var parts []string
	for _, a := range as {
		name := strings.TrimSpace(strings.TrimSpace(a.Given) + " " + strings.TrimSpace(a.Family))
		if name != "" {
			parts = append(parts, name)
		}
	}
	return strings.Join(parts, "; ")
}

func affiliationsText(as []model.Author) string {
	seen := map[string]bool{}
	var parts []string
	for _, a := range as {
		for _, aff := range a.Affiliation {
			if aff != "" && !seen[aff] {
				seen[aff] = true
				parts = append(parts, aff)
			}
		}
	}
	return strings.Join(parts, "; ")
}

func venueText(v *model.Venue) string {
	if v == nil {
		return ""
	}
	if v.Short != "" && v.Short != v.Name {
		return v.Name + " (" + v.Short + ")"
	}
	return v.Name
}

func readTextTree(root string, exts []string) string {
	var b strings.Builder
	var files []string
	walkFiles(root, func(path string) {
		for _, e := range exts {
			if strings.EqualFold(filepath.Ext(path), e) {
				files = append(files, path)
				return
			}
		}
	})
	sort.Strings(files)
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err == nil {
			b.Write(data)
			b.WriteString("\n")
		}
	}
	return b.String()
}

func readWebTree(root string) string {
	var b strings.Builder
	var files []string
	walkFiles(root, func(path string) {
		switch strings.ToLower(filepath.Ext(path)) {
		case ".html", ".htm", ".md", ".txt":
			files = append(files, path)
		}
	})
	sort.Strings(files)
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if strings.HasSuffix(strings.ToLower(f), ".html") || strings.HasSuffix(strings.ToLower(f), ".htm") {
			b.WriteString(stripHTML(string(data)))
		} else {
			b.Write(data)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func stripHTML(s string) string {
	var b strings.Builder
	i := 0
	lower := strings.ToLower(s)
	for i < len(s) {
		if s[i] != '<' {
			b.WriteByte(s[i])
			i++
			continue
		}
		j := strings.IndexByte(s[i:], '>')
		if j < 0 {
			break
		}
		tag := strings.ToLower(strings.TrimSpace(s[i+1 : i+j]))
		if strings.HasPrefix(tag, "script") || strings.HasPrefix(tag, "style") {
			closing := "</script>"
			if strings.HasPrefix(tag, "style") {
				closing = "</style>"
			}
			if end := strings.Index(lower[i:], closing); end >= 0 {
				i += end + len(closing)
			} else {
				i = len(s)
			}
			b.WriteByte(' ')
			continue
		}
		i += j + 1
		b.WriteByte(' ')
	}
	// Collapse runs of whitespace.
	return strings.Join(strings.Fields(b.String()), " ")
}
