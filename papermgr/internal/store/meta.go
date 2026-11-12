package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"papermgr/internal/model"
)

// MetaShow returns the paper plus a sorted provenance view.
func (s *Store) MetaShow(citekey string) (*model.Paper, error) { return s.Load(citekey) }

// MetaSet updates one field manually and locks its provenance.
func (s *Store) MetaSet(citekey, field, value string) error {
	p, err := s.Load(citekey)
	if err != nil {
		return err
	}
	s.detectManualEdits(p)
	field = strings.TrimSpace(field)
	if field == "" {
		return errors.New("empty field name")
	}
	if err := setMetaField(&p.Meta, field, value); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	p.State.Fields[field] = model.FieldState{Source: "manual", At: now, Locked: true}
	if err := s.Save(p); err != nil {
		return err
	}
	s.Log.Info("meta.set", map[string]interface{}{"citekey": citekey, "field": field})
	if err := s.SyncPaper(citekey); err != nil {
		return err
	}
	s.autoBib()
	return nil
}

func setMetaField(m *model.Meta, field, value string) error {
	value = strings.TrimSpace(value)
	switch field {
	case "title":
		m.Title = value
	case "abstract":
		m.Abstract = value
	case "doi":
		m.DOI = normalizeDOI(value)
	case "arxiv_id", "arxiv":
		m.ArxivID = value
	case "url":
		m.URL = value
	case "entry_type", "type":
		allowed := map[string]bool{"article": true, "inproceedings": true, "incollection": true, "book": true, "phdthesis": true, "techreport": true, "preprint": true, "misc": true}
		if !allowed[value] {
			return fmt.Errorf("invalid entry_type %q", value)
		}
		m.EntryType = value
	case "language":
		m.Language = value
	case "year":
		y, err := strconv.Atoi(value)
		if err != nil || y < 0 || y > 3000 {
			return fmt.Errorf("invalid year %q", value)
		}
		m.Year = &y
	case "keywords":
		m.Keywords = splitList(value)
	case "authors":
		var authors []model.Author
		if strings.HasPrefix(value, "[") {
			if err := json.Unmarshal([]byte(value), &authors); err != nil {
				return fmt.Errorf("invalid authors JSON: %w", err)
			}
		} else {
			for _, part := range strings.Split(value, ";") {
				part = strings.TrimSpace(part)
				if part == "" {
					continue
				}
				a := model.Author{}
				if i := strings.Index(part, ","); i >= 0 {
					a.Family = strings.TrimSpace(part[:i])
					a.Given = strings.TrimSpace(part[i+1:])
				} else {
					fields := strings.Fields(part)
					if len(fields) > 0 {
						a.Family = fields[len(fields)-1]
						a.Given = strings.Join(fields[:len(fields)-1], " ")
					}
				}
				authors = append(authors, a)
			}
		}
		m.Authors = authors
	case "venue":
		if m.Venue == nil {
			m.Venue = &model.Venue{}
		}
		m.Venue.Name = value
	default:
		if strings.HasPrefix(field, "venue.") {
			if m.Venue == nil {
				m.Venue = &model.Venue{}
			}
			sub := strings.TrimPrefix(field, "venue.")
			switch sub {
			case "name":
				m.Venue.Name = value
			case "short":
				m.Venue.Short = value
			case "kind":
				m.Venue.Kind = value
			case "volume":
				m.Venue.Volume = strPtr(value)
			case "issue":
				m.Venue.Issue = strPtr(value)
			case "pages":
				m.Venue.Pages = strPtr(value)
			case "publisher":
				m.Venue.Publisher = strPtr(value)
			default:
				return fmt.Errorf("unknown venue field %q", sub)
			}
			return nil
		}
		return fmt.Errorf("unknown field %q (try title, year, doi, abstract, authors, keywords, venue.name, ...)", field)
	}
	return nil
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// RefreshResult reports one paper's refresh outcome.
type RefreshResult struct {
	Citekey  string   `json:"citekey"`
	Changed  []string `json:"changed,omitempty"`
	Conflict []string `json:"conflict,omitempty"`
	Status   string   `json:"status"`
	Source   string   `json:"source,omitempty"`
	Skipped  []string `json:"skipped,omitempty"`
	Error    string   `json:"error,omitempty"`
}

// MetaRefresh re-fetches metadata. Locked fields are preserved unless force.
func (s *Store) MetaRefresh(citekey string, force bool) (RefreshResult, error) {
	res := RefreshResult{Citekey: citekey}
	p, err := s.Load(citekey)
	if err != nil {
		return res, err
	}
	s.detectManualEdits(p)
	sha := p.Meta.PDF.SHA256
	if sha == "" {
		sha, _ = HashFile(p.Dir + "/paper.pdf")
	}
	hints := inspectPDF(p.Dir + "/paper.pdf")
	candidates := s.Plugins.FetchMetadata(p.Dir+"/paper.pdf", sha, hints.mapValue())
	if len(candidates) == 0 {
		candidates = s.builtinCandidates(p.Dir+"/paper.pdf", sha, hints)
	} else {
		candidates = append(candidates, s.builtinCandidates(p.Dir+"/paper.pdf", sha, hints)...)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	var conflicts []model.Candidate
	claimed := map[string]bool{}
	for _, c := range candidates {
		tmp := model.Meta{}
		applyCandidate(&tmp, c.Fields, c.Source, map[string]string{})
		if c.Confidence < 0.7 {
			// Low-confidence guesses are review material only.
			conflicts = append(conflicts, model.Candidate{Source: c.Source, Confidence: c.Confidence, Title: c.Title, Fields: c.Fields})
			continue
		}
		conflicted := false
		for _, f := range []string{"title", "year", "doi", "arxiv_id", "url", "abstract", "entry_type", "language", "venue", "authors", "keywords"} {
			if !fieldProvided(tmp, f) || claimed[f] {
				continue
			}
			claimed[f] = true
			if p.State.Fields[f].Locked && !force {
				res.Skipped = append(res.Skipped, f)
				continue
			}
			oldVal := fieldString(p.Meta, f)
			newVal := fieldString(tmp, f)
			if oldVal == newVal {
				continue
			}
			if oldVal != "" && !p.State.Fields[f].Locked {
				if c.Priority <= -1000 {
					// The local filename fallback never overwrites real metadata.
					continue
				}
				if (f == "doi" || f == "arxiv_id") && !force {
					// Never silently change the paper's identity; ask the user.
					conflicted = true
					continue
				}
				conflicted = true
			}
			copyField(&p.Meta, tmp, f)
			p.State.Fields[f] = model.FieldState{Source: c.Source, At: now, Locked: p.State.Fields[f].Locked}
			res.Changed = append(res.Changed, f)
		}
		if conflicted {
			conflicts = append(conflicts, model.Candidate{Source: c.Source, Confidence: c.Confidence, Title: c.Title, Fields: c.Fields})
		}
	}
	if err := s.Save(p); err != nil {
		return res, err
	}
	p.State.Review.Candidates = conflicts
	if len(conflicts) > 0 {
		p.State.Review.Status = "pending"
		p.State.Review.Reason = fmt.Sprintf("%d metadata candidates need review", len(conflicts))
	}
	res.Conflict = uniqueStrings(conflictsSources(conflicts))
	res.Status = p.State.Review.Status
	if len(candidates) > 0 {
		res.Source = candidates[0].Source
	}
	if err := s.SaveState(p); err != nil {
		return res, err
	}
	if err := s.SyncPaper(citekey); err != nil {
		return res, err
	}
	s.Log.Info("meta.refresh", map[string]interface{}{"citekey": citekey, "changed": res.Changed, "status": res.Status})
	s.autoBib()
	return res, nil
}

// MetaRefreshAll refreshes every paper (or only pending ones).
func (s *Store) MetaRefreshAll(force, pendingOnly bool) []RefreshResult {
	keys, _ := s.AllCitekeys()
	var out []RefreshResult
	for _, ck := range keys {
		if pendingOnly {
			p, err := s.Load(ck)
			if err != nil || p.State.Review.Status != "pending" {
				continue
			}
		}
		r, err := s.MetaRefresh(ck, force)
		if err != nil {
			r.Error = err.Error()
		}
		out = append(out, r)
	}
	return out
}

// ReviewAccept applies candidate fields to a pending paper.
func (s *Store) ReviewAccept(citekey string, candidateIdx int) error {
	p, err := s.Load(citekey)
	if err != nil {
		return err
	}
	s.detectManualEdits(p)
	if p.State.Review.Status != "pending" || len(p.State.Review.Candidates) == 0 {
		return errors.New("paper has no pending metadata candidates")
	}
	if candidateIdx < 0 || candidateIdx >= len(p.State.Review.Candidates) {
		return fmt.Errorf("candidate %d out of range", candidateIdx)
	}
	c := p.State.Review.Candidates[candidateIdx]
	tmp := model.Meta{}
	applyCandidate(&tmp, c.Fields, c.Source, map[string]string{})
	now := time.Now().UTC().Format(time.RFC3339)
	for _, f := range []string{"title", "year", "doi", "arxiv_id", "url", "abstract", "entry_type", "language", "venue", "authors", "keywords"} {
		if !fieldProvided(tmp, f) {
			continue
		}
		if p.State.Fields[f].Locked {
			continue
		}
		copyField(&p.Meta, tmp, f)
		p.State.Fields[f] = model.FieldState{Source: c.Source, At: now}
	}
	p.State.Review.Status = "done"
	p.State.Review.Candidates = nil
	p.State.Review.Reason = ""
	if err := s.Save(p); err != nil {
		return err
	}
	s.Log.Info("review.accept", map[string]interface{}{"citekey": citekey, "source": c.Source})
	if err := s.SyncPaper(citekey); err != nil {
		return err
	}
	s.autoBib()
	return nil
}

// ReviewKeep marks a pending paper as reviewed without changing metadata.
func (s *Store) ReviewKeep(citekey string) error {
	p, err := s.Load(citekey)
	if err != nil {
		return err
	}
	s.detectManualEdits(p)
	p.State.Review.Status = "done"
	p.State.Review.Candidates = nil
	p.State.Review.Reason = ""
	if err := s.SaveState(p); err != nil {
		return err
	}
	return nil
}

// PendingPapers lists papers awaiting metadata review.
func (s *Store) PendingPapers() ([]*model.Paper, error) {
	keys, err := s.AllCitekeys()
	if err != nil {
		return nil, err
	}
	var out []*model.Paper
	for _, ck := range keys {
		p, err := s.Load(ck)
		if err == nil && p.State.Review.Status == "pending" {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Meta.AddedAt > out[j].Meta.AddedAt })
	return out, nil
}

func fieldProvided(m model.Meta, f string) bool { return fieldString(m, f) != "" }

func fieldString(m model.Meta, f string) string {
	switch f {
	case "title":
		return m.Title
	case "year":
		if m.Year == nil {
			return ""
		}
		return strconv.Itoa(*m.Year)
	case "doi":
		return m.DOI
	case "arxiv_id":
		return m.ArxivID
	case "url":
		return m.URL
	case "abstract":
		return m.Abstract
	case "entry_type":
		return m.EntryType
	case "language":
		return m.Language
	case "venue":
		if m.Venue == nil {
			return ""
		}
		b, _ := json.Marshal(m.Venue)
		return string(b)
	case "authors":
		if len(m.Authors) == 0 {
			return ""
		}
		b, _ := json.Marshal(m.Authors)
		return string(b)
	case "keywords":
		if len(m.Keywords) == 0 {
			return ""
		}
		b, _ := json.Marshal(m.Keywords)
		return string(b)
	}
	return ""
}

func copyField(dst *model.Meta, src model.Meta, f string) {
	switch f {
	case "title":
		dst.Title = src.Title
	case "year":
		dst.Year = src.Year
	case "doi":
		dst.DOI = src.DOI
	case "arxiv_id":
		dst.ArxivID = src.ArxivID
	case "url":
		dst.URL = src.URL
	case "abstract":
		dst.Abstract = src.Abstract
	case "entry_type":
		if src.EntryType != "" {
			dst.EntryType = src.EntryType
		}
	case "language":
		dst.Language = src.Language
	case "venue":
		dst.Venue = src.Venue
	case "authors":
		dst.Authors = src.Authors
	case "keywords":
		dst.Keywords = src.Keywords
	}
}

func conflictsSources(cs []model.Candidate) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Source)
	}
	return out
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// ReviewAcceptFields applies selected fields from a candidate, leaving the
// others untouched (FR-2.6 per-field acceptance).
func (s *Store) ReviewAcceptFields(citekey string, candidateIdx int, fields []string) error {
	p, err := s.Load(citekey)
	if err != nil {
		return err
	}
	s.detectManualEdits(p)
	if len(fields) == 0 {
		return fmt.Errorf("no fields selected for acceptance")
	}
	if candidateIdx < 0 || candidateIdx >= len(p.State.Review.Candidates) {
		return fmt.Errorf("candidate %d out of range", candidateIdx)
	}
	c := p.State.Review.Candidates[candidateIdx]
	tmp := model.Meta{}
	applyCandidate(&tmp, c.Fields, c.Source, map[string]string{})
	now := time.Now().UTC().Format(time.RFC3339)
	applied := 0
	for _, f := range fields {
		if !fieldProvided(tmp, f) || p.State.Fields[f].Locked {
			continue
		}
		copyField(&p.Meta, tmp, f)
		p.State.Fields[f] = model.FieldState{Source: c.Source, At: now}
		applied++
	}
	p.State.Review.Status = "done"
	p.State.Review.Candidates = nil
	p.State.Review.Reason = ""
	if err := s.Save(p); err != nil {
		return err
	}
	if err := s.SyncPaper(citekey); err != nil {
		return err
	}
	s.Log.Info("review.accept_fields", map[string]interface{}{"citekey": citekey, "source": c.Source, "fields": fields, "applied": applied})
	s.autoBib()
	return nil
}
