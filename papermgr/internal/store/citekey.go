package store

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"papermgr/internal/model"
)

var stopWords = map[string]bool{
	"a": true, "an": true, "the": true, "of": true, "for": true, "on": true,
	"in": true, "to": true, "and": true, "with": true, "is": true, "are": true,
	"be": true, "by": true, "at": true, "from": true, "as": true, "via": true,
	"towards": true, "toward": true, "using": true, "into": true, "we": true,
}

// Slug normalises arbitrary text into [a-z0-9-]+ per FR-3.2.
func Slug(s string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if unicode.IsSpace(r) || r == '-' || r == '_' || r == '/' || r == '.' || r == ':' || r == ',' {
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
			continue
		}
		// Any other character is a separator too.
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	return out
}

func titleWords(title string, n int) []string {
	var meaningful, all []string
	for _, w := range strings.Fields(title) {
		w = strings.Trim(w, ".,;:!?()[]{}'\"")
		if w == "" {
			continue
		}
		all = append(all, w)
		if !stopWords[strings.ToLower(w)] {
			meaningful = append(meaningful, w)
		}
	}
	use := meaningful
	if len(use) == 0 {
		use = all
	}
	if n > 0 && len(use) > n {
		use = use[:n]
	}
	return use
}

func firstFamily(m model.Meta) string {
	for _, a := range m.Authors {
		if strings.TrimSpace(a.Family) != "" {
			return a.Family
		}
	}
	return ""
}

func allFamilies(m model.Meta) string {
	var names []string
	for _, a := range m.Authors {
		if f := strings.TrimSpace(a.Family); f != "" {
			names = append(names, f)
		}
	}
	return strings.Join(names, "-")
}

func venueShort(m model.Meta) string {
	if m.Venue == nil {
		return ""
	}
	if m.Venue.Short != "" {
		return m.Venue.Short
	}
	return m.Venue.Name
}

// GenerateCitekey expands the configured template for m (no uniqueness check).
func (s *Store) GenerateCitekey(m model.Meta) string {
	tmpl := s.Cfg.CitekeyTemplate
	if tmpl == "" {
		tmpl = "{author}-{year}-{title}"
	}
	year := "nd"
	if m.Year != nil {
		year = strconv.Itoa(*m.Year)
	}
	title := strings.Join(titleWords(m.Title, s.Cfg.TitleWords), " ")
	repl := map[string]string{
		"{author}":  firstFamily(m),
		"{authors}": allFamilies(m),
		"{year}":    year,
		"{journal}": venueShort(m),
		"{title}":   title,
		"{type}":    m.EntryType,
	}
	out := tmpl
	for k, v := range repl {
		out = strings.ReplaceAll(out, k, v)
	}
	key := Slug(out)
	if key == "" {
		key = "paper"
	}
	if len(key) > 120 {
		key = strings.Trim(key[:120], "-")
	}
	return key
}

// UniqueCitekey finds a free citekey, appending title word then -2, -3 as
// required by FR-3.3. exclude lets rekey ignore the paper's current directory.
func (s *Store) UniqueCitekey(base string, m model.Meta, exclude string) string {
	taken := func(k string) bool {
		if k == "" {
			return true
		}
		if exclude != "" && k == exclude {
			return false
		}
		return s.Exists(k)
	}
	if !taken(base) {
		return base
	}
	if words := titleWords(m.Title, s.Cfg.TitleWords+1); len(words) > 0 {
		first := Slug(words[0])
		if first != "" && !strings.Contains(base, first) {
			if cand := base + "-" + first; !taken(cand) {
				return cand
			}
		}
	}
	for i := 2; i < 10000; i++ {
		cand := fmt.Sprintf("%s-%d", base, i)
		if !taken(cand) {
			return cand
		}
	}
	return base + "-x"
}
