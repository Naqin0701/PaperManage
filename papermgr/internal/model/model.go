// Package model defines the on-disk data types of PaperManage.
package model

import "encoding/json"

// SchemaVersion is the current meta.json / state.json schema version.
const SchemaVersion = 1

// Author is a paper author.
type Author struct {
	Given       string   `json:"given"`
	Family      string   `json:"family"`
	ORCID       *string  `json:"orcid"`
	Affiliation []string `json:"affiliation"`
}

// Venue describes where a paper was published.
type Venue struct {
	Name      string  `json:"name"`
	Short     string  `json:"short"`
	Kind      string  `json:"kind"` // journal | conference | book | other
	Volume    *string `json:"volume"`
	Issue     *string `json:"issue"`
	Pages     *string `json:"pages"`
	Publisher *string `json:"publisher"`
}

// PDFInfo describes the canonical paper.pdf.
type PDFInfo struct {
	Filename     string `json:"filename"`
	OriginalName string `json:"original_name"`
	SHA256       string `json:"sha256"`
	Pages        int    `json:"pages"`
	Size         int64  `json:"size"`
}

// Meta is the content of papers/<citekey>/meta.json. It is the single source
// of truth for metadata and may be edited by hand.
type Meta struct {
	SchemaVersion int      `json:"schema_version"`
	ID            string   `json:"id"`
	Citekey       string   `json:"citekey"`
	EntryType     string   `json:"entry_type"`
	Title         string   `json:"title"`
	Authors       []Author `json:"authors"`
	Year          *int     `json:"year,omitempty"`
	Venue         *Venue   `json:"venue,omitempty"`
	DOI           string   `json:"doi,omitempty"`
	ArxivID       string   `json:"arxiv_id,omitempty"`
	URL           string   `json:"url,omitempty"`
	Abstract      string   `json:"abstract,omitempty"`
	Keywords      []string `json:"keywords,omitempty"`
	Language      string   `json:"language,omitempty"`
	PDF           PDFInfo  `json:"pdf"`
	Tags          []string `json:"tags"`
	AddedAt       string   `json:"added_at"`
	UpdatedAt     string   `json:"updated_at"`
}

// FieldState is provenance for one metadata field.
type FieldState struct {
	Source string `json:"source"`
	At     string `json:"at"`
	Locked bool   `json:"locked"`
}

// Candidate is a metadata proposal retained for review.
type Candidate struct {
	Source     string                 `json:"source"`
	Confidence float64                `json:"confidence"`
	Title      string                 `json:"title,omitempty"`
	Fields     map[string]interface{} `json:"fields,omitempty"`
}

// ReviewState records the confidence decision made at ingest time.
type ReviewState struct {
	Status     string      `json:"status"` // done | pending
	Reason     string      `json:"reason,omitempty"`
	Candidates []Candidate `json:"candidates,omitempty"`
}

// IndexState records extraction results for a paper.
type IndexState struct {
	ContentSHA256 string  `json:"content_sha256"`
	ExtractedAt   string  `json:"extracted_at"`
	MDSHA256      *string `json:"md_sha256"`
	Error         string  `json:"error,omitempty"`
}

// State is papers/<citekey>/.papermgr/state.json. Deleting it loses only
// provenance, locks and review state.
type State struct {
	SchemaVersion int                   `json:"schema_version"`
	CitekeyLocked bool                  `json:"citekey_locked"`
	Fields        map[string]FieldState `json:"fields"`
	Review        ReviewState           `json:"review"`
	Index         IndexState            `json:"index"`
	// MetaSHA256 and MetaSnapshot let reindex detect hand edits to meta.json
	// and lock only the fields the user changed.
	MetaSHA256   string          `json:"meta_sha256,omitempty"`
	MetaSnapshot json.RawMessage `json:"meta_snapshot,omitempty"`
}

// Paper bundles a metadata record with its derived state and directory.
type Paper struct {
	Meta  Meta   `json:"meta"`
	State State  `json:"state"`
	Dir   string `json:"dir"` // absolute directory
}

// TagCount is one node in the tag tree.
type TagCount struct {
	Tag      string `json:"tag"`
	Count    int    `json:"count"` // includes descendants
	Explicit int    `json:"explicit"`
}

// SearchHit is an index row returned by the query layer.
type SearchHit struct {
	ID        string   `json:"id"`
	Citekey   string   `json:"citekey"`
	Title     string   `json:"title"`
	Authors   string   `json:"authors,omitempty"`
	Year      *int     `json:"year,omitempty"`
	Venue     string   `json:"venue,omitempty"`
	Tags      []string `json:"tags"`
	DOI       string   `json:"doi,omitempty"`
	HasNote   bool     `json:"has_note"`
	HasPDF    bool     `json:"has_pdf"`
	AddedAt   string   `json:"added_at"`
	UpdatedAt string   `json:"updated_at,omitempty"`
	Score     float64  `json:"score"`
}

// LinkCandidate is one metadata proposal from a plugin.
type LinkCandidate struct {
	Source     string
	Priority   int
	Confidence float64
	Fields     map[string]interface{}
	Title      string
}
