package store

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"papermgr/internal/config"
	"papermgr/internal/model"
	"papermgr/internal/pdftext"
	"papermgr/internal/ulid"
)

// AddOptions controls ingest.
type AddOptions struct {
	Replace bool
	Tags    []string
	Yes     bool
}

// AddResult reports what happened to one input file.
type AddResult struct {
	Citekey    string
	ID         string
	Duplicate  bool
	Replaced   bool
	Review     string
	Confidence float64
	Title      string
	Source     string
	Warnings   []string
}

var doiRe = regexp.MustCompile(`10\.[0-9]{4,9}/[^\s<>"']+`)
var arxivRe = regexp.MustCompile(`(?i)arxiv:\s*([0-9]{4}\.[0-9]{4,5})`)
var yearRe = regexp.MustCompile(`(19[5-9][0-9]|20[0-9]{2})`)

// AddPDF ingests one PDF. The file is copied, never moved.
func (s *Store) AddPDF(path string, opts AddOptions) (AddResult, error) {
	var res AddResult
	abs, err := filepath.Abs(path)
	if err != nil {
		return res, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return res, fmt.Errorf("stat %s: %w", path, err)
	}
	if st.IsDir() {
		return res, fmt.Errorf("%s is a directory", path)
	}
	header := make([]byte, 1024)
	n := 0
	if f, err := os.Open(abs); err == nil {
		n, _ = io.ReadFull(f, header)
		f.Close()
	}
	if !bytes.Contains(header[:n], []byte("%PDF-")) {
		return res, fmt.Errorf("%s is not a PDF (missing %%PDF- header)", path)
	}
	for _, t := range opts.Tags {
		if err := ValidateTag(t); err != nil {
			return res, err
		}
	}
	sha, err := HashFile(abs)
	if err != nil {
		return res, err
	}
	if dup, err := s.FindBySHA(sha); err != nil {
		return res, err
	} else if dup != nil {
		return s.handleDuplicate(dup, abs, sha, opts)
	}

	hints := inspectPDF(abs)
	candidates := s.Plugins.FetchMetadata(abs, sha, hints.mapValue())
	if len(candidates) == 0 {
		candidates = s.builtinCandidates(abs, sha, hints)
	} else {
		// Always keep the local fallback at lowest priority to fill gaps.
		candidates = append(candidates, s.builtinCandidates(abs, sha, hints)...)
	}
	meta := model.Meta{
		SchemaVersion: model.SchemaVersion,
		ID:            ulid.New(),
		EntryType:     "article",
		Authors:       []model.Author{},
		PDF: model.PDFInfo{
			Filename:     "paper.pdf",
			OriginalName: filepath.Base(abs),
			SHA256:       sha,
			Size:         st.Size(),
			Pages:        hints.pages,
		},
		Tags:    []string{},
		AddedAt: time.Now().Format(time.RFC3339),
	}
	applied := map[string]string{}
	var reviewCandidates []model.Candidate
	bestConf := 0.0
	bestSource := ""
	for _, c := range candidates {
		// Candidates below the "medium" threshold are kept for review but must
		// not overwrite the placeholder derived from the PDF itself.
		if c.Confidence >= 0.7 {
			if c.Confidence > bestConf {
				bestConf = c.Confidence
				bestSource = c.Source
			}
			applyCandidate(&meta, c.Fields, c.Source, applied)
		}
		if c.Confidence < 0.9 {
			reviewCandidates = append(reviewCandidates, model.Candidate{
				Source: c.Source, Confidence: c.Confidence, Title: c.Title, Fields: c.Fields,
			})
		}
	}
	if strings.TrimSpace(meta.Title) == "" {
		meta.Title = titleFromFilename(abs)
		applied["title"] = "filename"
	}
	if len(opts.Tags) > 0 {
		meta.Tags = dedupe(append(meta.Tags, opts.Tags...))
	}

	// DOI dedupe (FR-1.3).
	meta.DOI = normalizeDOI(meta.DOI)
	if meta.DOI != "" {
		if dup, err := s.FindByDOI(meta.DOI); err != nil {
			return res, err
		} else if dup != nil {
			return s.handleDuplicate(dup, abs, sha, opts)
		}
	}

	base := s.GenerateCitekey(meta)
	meta.Citekey = s.UniqueCitekey(base, meta, "")
	dir := s.PaperDir(meta.Citekey)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return res, err
	}
	for _, sub := range []string{"attachments/note", "attachments/supplement", "attachments/web", "attachments/misc", "attachments/markdown", ".papermgr"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return res, err
		}
	}
	dst := filepath.Join(dir, "paper.pdf")
	if err := copyFile(abs, dst, 0o644); err != nil {
		_ = os.RemoveAll(dir)
		return res, err
	}

	state := DefaultState()
	now := time.Now().UTC().Format(time.RFC3339)
	for field, src := range applied {
		if src == "" {
			continue
		}
		state.Fields[field] = model.FieldState{Source: src, At: now}
	}
	state.Review.Candidates = reviewCandidates
	if meta.Title == titleFromFilename(abs) && bestConf < 0.9 {
		state.Review.Status = "pending"
		state.Review.Reason = "no metadata match; title taken from filename"
	} else if bestConf >= 0.9 && meta.DOI != "" {
		state.Review.Status = "done"
	} else if bestConf >= 0.7 {
		state.Review.Status = "pending"
		state.Review.Reason = fmt.Sprintf("medium confidence %.2f from %s", bestConf, bestSource)
	} else {
		state.Review.Status = "pending"
		state.Review.Reason = fmt.Sprintf("low confidence %.2f from %s", bestConf, bestSource)
	}

	if state.Review.Status != "pending" {
		state.Review.Candidates = nil
		state.Review.Reason = ""
	}

	p := &model.Paper{Meta: meta, State: state, Dir: dir}
	if err := s.Save(p); err != nil {
		_ = os.RemoveAll(dir)
		return res, err
	}
	if err := s.SyncPaper(meta.Citekey); err != nil {
		res.Warnings = append(res.Warnings, "index update failed: "+err.Error())
		s.Log.Warn("add.index_failed", map[string]interface{}{"citekey": meta.Citekey, "error": err.Error()})
	}
	s.autoBib()
	s.Log.Info("paper.added", map[string]interface{}{
		"citekey": meta.Citekey, "id": meta.ID, "sha256": sha, "source": bestSource,
		"review": state.Review.Status, "confidence": bestConf,
	})
	res.Citekey = meta.Citekey
	res.ID = meta.ID
	res.Review = state.Review.Status
	res.Confidence = bestConf
	res.Title = meta.Title
	res.Source = bestSource
	return res, nil
}

func (s *Store) handleDuplicate(dup *model.Paper, newPath, sha string, opts AddOptions) (AddResult, error) {
	res := AddResult{Citekey: dup.Meta.Citekey, ID: dup.Meta.ID, Duplicate: true, Title: dup.Meta.Title, Review: dup.State.Review.Status}
	if opts.Replace {
		target := filepath.Join(dup.Dir, "paper.pdf")
		if sameFile(target, newPath) {
			res.Replaced = true
			res.Warnings = append(res.Warnings, "source is already this paper's paper.pdf; nothing to replace")
			return res, nil
		}
	}
	misc := filepath.Join(dup.Dir, "attachments", "misc")
	if err := os.MkdirAll(misc, 0o755); err != nil {
		return res, err
	}
	if opts.Replace {
		archive := filepath.Join(misc, fmt.Sprintf("paper-%s.pdf", shortSHA(dup.Meta.PDF.SHA256)))
		if !fileExists(archive) {
			if err := os.Rename(filepath.Join(dup.Dir, "paper.pdf"), archive); err != nil && !errors.Is(err, os.ErrNotExist) {
				return res, err
			}
		}
		if err := copyFile(newPath, filepath.Join(dup.Dir, "paper.pdf"), 0o644); err != nil {
			return res, err
		}
		st, _ := os.Stat(newPath)
		dup.Meta.PDF.SHA256 = sha
		dup.Meta.PDF.OriginalName = filepath.Base(newPath)
		if st != nil {
			dup.Meta.PDF.Size = st.Size()
		}
		if err := s.Save(dup); err != nil {
			return res, err
		}
		res.Replaced = true
		if len(opts.Tags) > 0 {
			dup.Meta.Tags = dedupe(append(dup.Meta.Tags, opts.Tags...))
			if err := s.Save(dup); err != nil {
				return res, err
			}
		}
		res.Warnings = append(res.Warnings, fmt.Sprintf("replaced paper.pdf for %s (old copy in attachments/misc)", dup.Meta.Citekey))
		_ = s.SyncPaper(dup.Meta.Citekey)
		s.autoBib()
		return res, nil
	}
	name := fmt.Sprintf("duplicate-%s-%s", shortSHA(sha), sanitizeFilename(filepath.Base(newPath)))
	dst := uniquePath(filepath.Join(misc, name))
	if err := copyFile(newPath, dst, 0o644); err != nil {
		return res, err
	}
	if len(opts.Tags) > 0 {
		dup.Meta.Tags = dedupe(append(dup.Meta.Tags, opts.Tags...))
		if err := s.Save(dup); err != nil {
			return res, err
		}
	}
	res.Warnings = append(res.Warnings, fmt.Sprintf("duplicate sha256, attached to %s", dup.Meta.Citekey))
	s.Log.Warn("paper.duplicate", map[string]interface{}{"citekey": dup.Meta.Citekey, "sha256": sha, "path": dst})
	_ = s.SyncPaper(dup.Meta.Citekey)
	return res, nil
}

func (h pdfHints) mapValue() map[string]string {
	m := map[string]string{"filename": h.title, "title_guess": h.title}
	if h.doi != "" {
		m["doi"] = h.doi
	}
	if h.arxiv != "" {
		m["arxiv_id"] = h.arxiv
	}
	if h.year > 0 {
		m["year"] = fmt.Sprint(h.year)
	}
	return m
}

type pdfHints struct {
	title  string
	author string
	doi    string
	arxiv  string
	year   int
	pages  int
	text   string
}

func inspectPDF(path string) pdfHints {
	h := parseFilename(path)
	text, err := pdftext.ExtractFirstPage(path)
	if err != nil {
		return h
	}
	h.text = text
	if m := doiRe.FindString(text); m != "" {
		h.doi = strings.TrimRight(m, ".,;:)]}")
	}
	if m := arxivRe.FindStringSubmatch(text); len(m) == 2 {
		h.arxiv = m[1]
	}
	return h
}

// parseFilename extracts a best-effort author/year/title from names such as
// "vaswani-2017-attention.pdf". It is deliberately conservative.
func parseFilename(path string) pdfHints {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	base = regexp.MustCompile(`^[0-9]{4}\.[0-9]{4,5}(v[0-9]+)?[-_ ]*`).ReplaceAllString(base, "")
	tokens := strings.FieldsFunc(base, func(r rune) bool {
		return r == '-' || r == '_' || r == ' ' || r == '.'
	})
	h := pdfHints{}
	yearIdx := -1
	for i, tok := range tokens {
		if yearRe.MatchString(tok) && len(tok) == 4 {
			if y, err := strconv.Atoi(tok); err == nil {
				h.year = y
				yearIdx = i
				break
			}
		}
	}
	if yearIdx > 0 && isAlphaToken(tokens[0]) {
		h.author = capitalize(tokens[0])
	}
	if yearIdx >= 0 && yearIdx+1 < len(tokens) {
		var words []string
		for _, tok := range tokens[yearIdx+1:] {
			if isAlphaToken(tok) || yearRe.MatchString(tok) {
				words = append(words, tok)
			}
		}
		h.title = strings.Join(words, " ")
	}
	if h.title == "" {
		h.title = titleFromFilename(path)
	}
	return h
}

func isAlphaToken(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsLetter(r) {
			return false
		}
	}
	return true
}

func capitalize(s string) string {
	r := []rune(strings.ToLower(s))
	if len(r) == 0 {
		return s
	}
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

func normalizeDOI(doi string) string {
	doi = strings.TrimSpace(doi)
	doi = strings.TrimPrefix(doi, "https://doi.org/")
	doi = strings.TrimPrefix(doi, "http://doi.org/")
	doi = strings.TrimPrefix(doi, "doi:")
	return strings.TrimRight(strings.TrimSpace(doi), ".,;")
}

func (s *Store) builtinCandidates(abs, sha string, h pdfHints) []model.LinkCandidate {
	fields := map[string]interface{}{}
	conf := 0.1
	source := "filename"
	if h.doi != "" {
		fields["doi"] = h.doi
		conf = 0.72
		source = "pdf-xmp"
	}
	if h.arxiv != "" {
		fields["arxiv_id"] = h.arxiv
		if h.doi == "" {
			conf = 0.70
			source = "pdf-xmp"
		}
	}
	if h.title != "" {
		fields["title"] = h.title
	}
	if h.year > 0 {
		fields["year"] = h.year
	}
	if h.author != "" {
		fields["authors"] = []interface{}{map[string]interface{}{"given": "", "family": h.author}}
	}
	return []model.LinkCandidate{{Source: source, Priority: -1000, Confidence: conf, Fields: fields, Title: h.title}}
}

func applyCandidate(m *model.Meta, fields map[string]interface{}, source string, applied map[string]string) {
	set := func(field string, ok bool) {
		if ok && applied[field] == "" {
			applied[field] = source
		}
	}
	if v, ok := strField(fields, "title"); ok && m.Title == "" {
		m.Title = v
		set("title", true)
	}
	if v, ok := intField(fields, "year"); ok && m.Year == nil {
		m.Year = &v
		set("year", true)
	}
	if v, ok := strField(fields, "doi"); ok && m.DOI == "" {
		m.DOI = normalizeDOI(v)
		set("doi", true)
	}
	if v, ok := strField(fields, "arxiv_id"); ok && m.ArxivID == "" {
		m.ArxivID = v
		set("arxiv_id", true)
	}
	if v, ok := strField(fields, "url"); ok && m.URL == "" {
		m.URL = v
		set("url", true)
	}
	if v, ok := strField(fields, "abstract"); ok && m.Abstract == "" {
		m.Abstract = v
		set("abstract", true)
	}
	if v, ok := strField(fields, "entry_type"); ok && v != "" {
		m.EntryType = v
		set("entry_type", true)
	}
	if v, ok := strField(fields, "language"); ok && m.Language == "" {
		m.Language = v
		set("language", true)
	}
	if m.Venue == nil {
		if vm, ok := fields["venue"].(map[string]interface{}); ok {
			v := &model.Venue{}
			v.Name, _ = strField(vm, "name")
			v.Short, _ = strField(vm, "short")
			v.Kind, _ = strField(vm, "kind")
			if s, ok := strField(vm, "volume"); ok {
				v.Volume = &s
			}
			if s, ok := strField(vm, "issue"); ok {
				v.Issue = &s
			}
			if s, ok := strField(vm, "pages"); ok {
				v.Pages = &s
			}
			if s, ok := strField(vm, "publisher"); ok {
				v.Publisher = &s
			}
			if v.Name != "" || v.Short != "" {
				m.Venue = v
				set("venue", true)
			}
		}
	}
	if len(m.Authors) == 0 {
		if raw, ok := fields["authors"].([]interface{}); ok {
			var authors []model.Author
			for _, item := range raw {
				am, ok := item.(map[string]interface{})
				if !ok {
					continue
				}
				a := model.Author{}
				a.Given, _ = strField(am, "given")
				a.Family, _ = strField(am, "family")
				if s, ok := strField(am, "orcid"); ok && s != "" {
					a.ORCID = &s
				}
				if affs, ok := am["affiliation"].([]interface{}); ok {
					for _, af := range affs {
						if s, ok := af.(string); ok {
							a.Affiliation = append(a.Affiliation, s)
						}
					}
				}
				if a.Given != "" || a.Family != "" {
					authors = append(authors, a)
				}
			}
			if len(authors) > 0 {
				m.Authors = authors
				set("authors", true)
			}
		}
	}
	if len(m.Keywords) == 0 {
		if raw, ok := fields["keywords"].([]interface{}); ok {
			for _, k := range raw {
				if s, ok := k.(string); ok && strings.TrimSpace(s) != "" {
					m.Keywords = append(m.Keywords, s)
				}
			}
			if len(m.Keywords) > 0 {
				set("keywords", true)
			}
		}
	}
}

func strField(m map[string]interface{}, key string) (string, bool) {
	v, ok := m[key]
	if !ok || v == nil {
		return "", false
	}
	s, ok := v.(string)
	if !ok {
		return fmt.Sprint(v), true
	}
	return strings.TrimSpace(s), s != ""
}

func intField(m map[string]interface{}, key string) (int, bool) {
	v, ok := m[key]
	if !ok || v == nil {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		if n > 0 {
			return int(n), true
		}
	case int:
		return n, n > 0
	case int64:
		return int(n), n > 0
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(n))
		return i, err == nil && i > 0
	}
	return 0, false
}

func titleFromFilename(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	base = regexp.MustCompile(`^[0-9]{4}\.[0-9]{4,5}(v[0-9]+)?[-_ ]*`).ReplaceAllString(base, "")
	base = strings.ReplaceAll(base, "_", " ")
	base = strings.ReplaceAll(base, "-", " ")
	base = strings.Join(strings.Fields(base), " ")
	if base == "" {
		return "Untitled"
	}
	return base
}

func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	return os.Rename(tmpName, dst)
}

func shortSHA(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

func sanitizeFilename(s string) string {
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "\\", "_")
	s = strings.TrimSpace(s)
	if s == "" {
		return "file"
	}
	return s
}

func uniquePath(path string) string {
	if !fileExists(path) {
		return path
	}
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	for i := 2; i < 1000; i++ {
		cand := fmt.Sprintf("%s-%d%s", base, i, ext)
		if !fileExists(cand) {
			return cand
		}
	}
	return path
}

func sameFile(a, b string) bool {
	sa, err := os.Stat(a)
	if err != nil {
		return false
	}
	sb, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(sa, sb)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func (s *Store) autoBib() {
	if err := s.ExportBib(); err != nil {
		s.Log.Warn("bib.export_failed", map[string]interface{}{"error": err.Error()})
	}
}

// SaveConfig persists library config.
func (s *Store) SaveConfig() error {
	return config.SaveLibrary(s.Root, s.Cfg)
}

// SortedTags returns a copy of tags sorted.
func SortedTags(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
