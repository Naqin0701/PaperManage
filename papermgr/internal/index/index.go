// Package index implements the rebuildable SQLite search index.
// It never reads or writes user metadata; the store layer supplies records.
package index

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "modernc.org/sqlite"

	"papermgr/internal/model"
	"papermgr/internal/pdftext"
	"papermgr/internal/query"
)

// Index is the SQLite search index.
type Index struct {
	db              *sql.DB
	path            string
	indexSupplement bool
}

// Record is one paper's indexable content.
type Record struct {
	ID           string
	Citekey      string
	EntryType    string
	Title        string
	Abstract     string
	Keywords     string
	Authors      string
	Affiliations string
	Venue        string
	Year         *int
	DOI          string
	ArxivID      string
	URL          string
	AddedAt      string
	UpdatedAt    string
	Tags         []string
	Content      string
	Note         string
	MD           string
	Web          string
	HasNote      bool
	IndexError   string
	Sig          string
	Pages        int
	MetaChanged  bool
}

// Open opens (creating if needed) <root>/library.sqlite.
func Open(root string, indexSupplements bool) (*Index, error) {
	path := filepath.Join(root, "library.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	x := &Index{db: db, path: path, indexSupplement: indexSupplements}
	if _, err := db.Exec(`PRAGMA journal_mode=WAL; PRAGMA synchronous=NORMAL; PRAGMA busy_timeout=5000;`); err != nil {
		db.Close()
		return nil, err
	}
	if err := x.create(); err != nil {
		db.Close()
		return nil, err
	}
	return x, nil
}

// Close closes the database.
func (x *Index) Close() error {
	if x == nil || x.db == nil {
		return nil
	}
	return x.db.Close()
}

// Path returns the sqlite file path.
func (x *Index) Path() string { return x.path }

// Exists reports whether the index exists on disk (it always does after Open).
func (x *Index) Exists() bool {
	st, err := os.Stat(x.path)
	return err == nil && st.Size() > 0
}

func (x *Index) create() error {
	_, err := x.db.Exec(`
CREATE TABLE IF NOT EXISTS papers (
  rowid INTEGER PRIMARY KEY,
  id TEXT UNIQUE NOT NULL,
  citekey TEXT UNIQUE NOT NULL,
  entry_type TEXT,
  title TEXT,
  abstract TEXT,
  keywords TEXT,
  authors TEXT,
  affiliations TEXT,
  venue TEXT,
  year INTEGER,
  doi TEXT,
  arxiv_id TEXT,
  url TEXT,
  added_at TEXT,
  updated_at TEXT,
  has_note INTEGER NOT NULL DEFAULT 0,
  index_error TEXT,
  sig TEXT
);
CREATE TABLE IF NOT EXISTS tags (
  paper_id TEXT NOT NULL,
  tag TEXT NOT NULL,
  PRIMARY KEY (paper_id, tag)
);
CREATE INDEX IF NOT EXISTS tags_tag_idx ON tags(tag);
CREATE VIRTUAL TABLE IF NOT EXISTS papers_fts USING fts5(
  title, abstract, keywords, authors, affiliations, venue,
  content, note, md, web,
  tokenize = 'unicode61 remove_diacritics 2'
);
CREATE TABLE IF NOT EXISTS kv (k TEXT PRIMARY KEY, v TEXT);
`)
	return err
}

// Rebuild drops and recreates all index tables.
func (x *Index) Rebuild() error {
	_, err := x.db.Exec(`
DROP TABLE IF EXISTS papers_fts;
DROP TABLE IF EXISTS tags;
DROP TABLE IF EXISTS papers;
DROP TABLE IF EXISTS kv;
`)
	if err != nil {
		return err
	}
	return x.create()
}

// Upsert inserts or updates one record and its full-text row.
func (x *Index) Upsert(r Record) error {
	tx, err := x.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var oldRow int64
	var oldID string
	err = tx.QueryRow(`SELECT rowid, id FROM papers WHERE id = ? OR citekey = ?`, r.ID, r.Citekey).Scan(&oldRow, &oldID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	hasNote := 0
	if r.HasNote {
		hasNote = 1
	}
	if oldRow != 0 {
		_, err = tx.Exec(`UPDATE papers SET id=?, citekey=?, entry_type=?, title=?, abstract=?, keywords=?,
			authors=?, affiliations=?, venue=?, year=?, doi=?, arxiv_id=?, url=?, added_at=?, updated_at=?,
			has_note=?, index_error=?, sig=? WHERE rowid=?`,
			r.ID, r.Citekey, r.EntryType, r.Title, r.Abstract, r.Keywords, r.Authors, r.Affiliations,
			r.Venue, r.Year, r.DOI, r.ArxivID, r.URL, r.AddedAt, r.UpdatedAt, hasNote, r.IndexError, r.Sig, oldRow)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM papers_fts WHERE rowid = ?`, oldRow); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM tags WHERE paper_id = ?`, oldID); err != nil {
			return err
		}
	} else {
		res, err := tx.Exec(`INSERT INTO papers (id, citekey, entry_type, title, abstract, keywords, authors,
			affiliations, venue, year, doi, arxiv_id, url, added_at, updated_at, has_note, index_error, sig)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			r.ID, r.Citekey, r.EntryType, r.Title, r.Abstract, r.Keywords, r.Authors, r.Affiliations,
			r.Venue, r.Year, r.DOI, r.ArxivID, r.URL, r.AddedAt, r.UpdatedAt, hasNote, r.IndexError, r.Sig)
		if err != nil {
			return err
		}
		oldRow, err = res.LastInsertId()
		if err != nil {
			return err
		}
	}
	_, err = tx.Exec(`INSERT INTO papers_fts (rowid, title, abstract, keywords, authors, affiliations, venue, content, note, md, web)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		oldRow, r.Title, r.Abstract, r.Keywords, r.Authors, r.Affiliations, r.Venue,
		r.Content, r.Note, r.MD, r.Web)
	if err != nil {
		return err
	}
	for _, t := range dedupeTags(r.Tags) {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO tags(paper_id, tag) VALUES (?,?)`, r.ID, t); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DeleteByID removes a paper and its FTS row.
func (x *Index) DeleteByID(id string) error {
	tx, err := x.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var row int64
	if err := tx.QueryRow(`SELECT rowid FROM papers WHERE id=?`, id).Scan(&row); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return tx.Commit()
		}
		return err
	}
	if _, err := tx.Exec(`DELETE FROM papers_fts WHERE rowid=?`, row); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM tags WHERE paper_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM papers WHERE rowid=?`, row); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteByCitekey removes a paper by citekey.
func (x *Index) DeleteByCitekey(citekey string) error {
	var id string
	err := x.db.QueryRow(`SELECT id FROM papers WHERE citekey=?`, citekey).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return x.DeleteByID(id)
}

// Signature returns the stored stat signature for a citekey.
func (x *Index) Signature(citekey string) (string, bool) {
	var sig string
	err := x.db.QueryRow(`SELECT COALESCE(sig,'') FROM papers WHERE citekey=?`, citekey).Scan(&sig)
	if err != nil {
		return "", false
	}
	return sig, true
}

// Signatures returns stored signatures for all indexed citekeys.
func (x *Index) Signatures() (map[string]string, error) {
	rows, err := x.db.Query(`SELECT citekey, COALESCE(sig,'') FROM papers`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var c, s string
		if err := rows.Scan(&c, &s); err != nil {
			return nil, err
		}
		out[c] = s
	}
	return out, rows.Err()
}

// Count returns the number of indexed papers.
func (x *Index) Count() int {
	var n int
	_ = x.db.QueryRow(`SELECT COUNT(*) FROM papers`).Scan(&n)
	return n
}

// SetMeta stores a key/value in kv.
func (x *Index) SetMeta(k, v string) error {
	_, err := x.db.Exec(`INSERT INTO kv(k,v) VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, k, v)
	return err
}

// GetMeta reads a kv value.
func (x *Index) GetMeta(k string) (string, bool) {
	var v string
	err := x.db.QueryRow(`SELECT v FROM kv WHERE k=?`, k).Scan(&v)
	return v, err == nil
}

// TagCounts computes explicit tags plus implicit ancestors. Counts are unique
// papers, so a paper tagged both "ml" and "ml/llm" contributes once to "ml".
func (x *Index) TagCounts() ([]model.TagCount, error) {
	rows, err := x.db.Query(`SELECT paper_id, tag FROM tags`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	paperSets := map[string]map[string]bool{}
	explicit := map[string]map[string]bool{}
	for rows.Next() {
		var id, tag string
		if err := rows.Scan(&id, &tag); err != nil {
			return nil, err
		}
		if explicit[tag] == nil {
			explicit[tag] = map[string]bool{}
		}
		explicit[tag][id] = true
		parts := strings.Split(tag, "/")
		for i := 1; i <= len(parts); i++ {
			anc := strings.Join(parts[:i], "/")
			if paperSets[anc] == nil {
				paperSets[anc] = map[string]bool{}
			}
			paperSets[anc][id] = true
		}
	}
	out := make([]model.TagCount, 0, len(paperSets))
	for tag, set := range paperSets {
		out = append(out, model.TagCount{Tag: tag, Count: len(set), Explicit: len(explicit[tag])})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Tag < out[j].Tag })
	return out, nil
}

// Query executes a search expression and returns hits.
func (x *Index) Query(expr string) ([]model.SearchHit, error) {
	q, err := query.Parse(expr)
	if err != nil {
		return nil, err
	}
	root := query.StripSort(q.Root)
	if hasContentLeaf(root) && !pdftext.Available() {
		return nil, pdftext.ErrUnavailable
	}
	where, args := compile(root)
	sqlStr := `SELECT id, citekey, title, COALESCE(authors,''), year, COALESCE(venue,''), COALESCE(doi,''),
		has_note, added_at, COALESCE(index_error,''), 0.0 FROM papers`
	if where != "" {
		sqlStr += " WHERE " + where
	}
	order := orderClause(q.Sort)
	if q.Sort == "relevance" {
		if ftsExpr, ok := positiveFTS(root); ok {
			sqlStr = strings.Replace(sqlStr, `, 0.0 FROM papers`,
				`, COALESCE((SELECT bm25(papers_fts, 3.0, 1.5, 2.0, 1.5, 1.0, 1.0, 1.0, 1.0) FROM papers_fts
					WHERE papers_fts.rowid = papers.rowid AND papers_fts MATCH ?), 0.0) AS score FROM papers`, 1)
			args = append([]interface{}{ftsExpr}, args...)
			order = "score ASC, added_at DESC"
		}
	}
	sqlStr += " ORDER BY " + order + " LIMIT 5000"
	rows, err := x.db.Query(sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	defer rows.Close()
	var hits []model.SearchHit
	for rows.Next() {
		var h model.SearchHit
		var year sql.NullInt64
		var note int
		var idxErr string
		if err := rows.Scan(&h.ID, &h.Citekey, &h.Title, &h.Authors, &year, &h.Venue, &h.DOI,
			&note, &h.AddedAt, &idxErr, &h.Score); err != nil {
			return nil, err
		}
		if year.Valid {
			v := int(year.Int64)
			h.Year = &v
		}
		h.HasNote = note == 1
		h.HasPDF = true
		hits = append(hits, h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Attach tags (cheap for 1000 rows).
	tagRows, err := x.db.Query(`SELECT paper_id, tag FROM tags ORDER BY tag`)
	if err == nil {
		defer tagRows.Close()
		m := map[string][]string{}
		for tagRows.Next() {
			var id, t string
			if tagRows.Scan(&id, &t) == nil {
				m[id] = append(m[id], t)
			}
		}
		for i := range hits {
			hits[i].Tags = m[hits[i].ID]
		}
	}
	return hits, nil
}

func orderClause(sortKey string) string {
	switch sortKey {
	case "year-":
		return "year IS NULL, year DESC, title COLLATE NOCASE"
	case "year+":
		return "year IS NULL, year ASC, title COLLATE NOCASE"
	case "added-":
		return "added_at DESC"
	case "added+":
		return "added_at ASC"
	case "updated-":
		return "updated_at DESC"
	case "updated+":
		return "updated_at ASC"
	case "title+":
		return "title COLLATE NOCASE ASC"
	case "title-":
		return "title COLLATE NOCASE DESC"
	default:
		return "added_at DESC"
	}
}

func hasContentLeaf(n query.Node) bool {
	found := false
	query.Walk(n, func(t *query.Term) {
		if t.Field == "content" {
			found = true
		}
	})
	return found
}

func dedupeTags(tags []string) []string {
	seen := map[string]bool{}
	out := tags[:0]
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// RenameCitekey updates a citekey without rebuilding the paper's FTS content.
func (x *Index) RenameCitekey(old, new string) error {
	_, err := x.db.Exec(`UPDATE papers SET citekey=? WHERE citekey=?`, new, old)
	return err
}
