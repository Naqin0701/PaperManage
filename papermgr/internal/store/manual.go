package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"papermgr/internal/model"
)

// detectManualEdits compares meta.json against the snapshot recorded in
// state.json. Changed fields are locked with source "manual" (section 5.2).
func (s *Store) detectManualEdits(p *model.Paper) {
	raw, err := os.ReadFile(filepath.Join(p.Dir, "meta.json"))
	if err != nil {
		return
	}
	hash := hashBytes(raw)
	if p.State.MetaSHA256 == "" {
		p.State.MetaSHA256 = hash
		p.State.MetaSnapshot = append([]byte(nil), raw...)
		return
	}
	if p.State.MetaSHA256 == hash {
		return
	}
	var old model.Meta
	if len(p.State.MetaSnapshot) > 0 {
		_ = json.Unmarshal(p.State.MetaSnapshot, &old)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	changed := []string{}
	for _, f := range []string{"title", "year", "doi", "arxiv_id", "url", "abstract", "entry_type", "language", "venue", "authors", "keywords"} {
		if fieldString(old, f) != fieldString(p.Meta, f) {
			changed = append(changed, f)
			fs := p.State.Fields[f]
			if !fs.Locked {
				p.State.Fields[f] = model.FieldState{Source: "manual", At: now, Locked: true}
			}
		}
	}
	p.State.MetaSHA256 = hash
	p.State.MetaSnapshot = append([]byte(nil), raw...)
	if len(changed) > 0 {
		s.Log.Info("meta.manual_edit", map[string]interface{}{"citekey": p.Meta.Citekey, "fields": changed})
	}
	_ = s.SaveState(p)
}
