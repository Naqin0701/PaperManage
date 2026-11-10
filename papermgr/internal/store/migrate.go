package store

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"papermgr/internal/config"
	"papermgr/internal/model"
)

// SchemaIssue describes a paper whose files use a different schema version.
type SchemaIssue struct {
	Citekey      string
	MetaVersion  int
	StateVersion int
}

// SchemaIssues scans meta.json and state.json for versions other than the
// current one. It never modifies files.
func (s *Store) SchemaIssues() ([]SchemaIssue, error) {
	keys, err := s.AllCitekeys()
	if err != nil {
		return nil, err
	}
	var out []SchemaIssue
	for _, ck := range keys {
		p, err := s.Load(ck)
		if err != nil {
			out = append(out, SchemaIssue{Citekey: ck, MetaVersion: -1})
			continue
		}
		if p.Meta.SchemaVersion != model.SchemaVersion || p.State.SchemaVersion != model.SchemaVersion {
			out = append(out, SchemaIssue{Citekey: ck, MetaVersion: p.Meta.SchemaVersion, StateVersion: p.State.SchemaVersion})
		}
	}
	return out, nil
}

// Migrate backs up affected metadata files under .trash/backup-<ts>/ and
// upgrades schema_version 0 documents to the current schema. It refuses to
// downgrade documents written by a newer version.
func (s *Store) Migrate() (int, string, error) {
	issues, err := s.SchemaIssues()
	if err != nil {
		return 0, "", err
	}
	for _, is := range issues {
		if is.MetaVersion > model.SchemaVersion || is.StateVersion > model.SchemaVersion {
			return 0, "", fmt.Errorf("paper %s uses a newer schema (%d); upgrade papermgr", is.Citekey, is.MetaVersion)
		}
	}
	if len(issues) == 0 {
		return 0, "", nil
	}
	ts := time.Now().Format("20060102T150405")
	backup := filepath.Join(s.Root, ".trash", "backup-"+ts)
	if err := os.MkdirAll(backup, 0o755); err != nil {
		return 0, "", err
	}
	for _, is := range issues {
		src := s.PaperDir(is.Citekey)
		dst := filepath.Join(backup, is.Citekey)
		if err := os.MkdirAll(filepath.Join(dst, ".papermgr"), 0o755); err != nil {
			return 0, "", err
		}
		for _, rel := range []string{"meta.json", filepath.Join(".papermgr", "state.json")} {
			data, err := os.ReadFile(filepath.Join(src, rel))
			if err != nil {
				continue
			}
			_ = config.AtomicWrite(filepath.Join(dst, rel), data, 0o644)
		}
		p, err := s.Load(is.Citekey)
		if err != nil {
			return 0, backup, err
		}
		p.Meta.SchemaVersion = model.SchemaVersion
		p.State.SchemaVersion = model.SchemaVersion
		if err := s.Save(p); err != nil {
			return 0, backup, err
		}
		if err := s.SyncPaper(is.Citekey); err != nil {
			return 0, backup, err
		}
	}
	s.Log.Info("schema.migrate", map[string]interface{}{"papers": len(issues), "backup": backup})
	return len(issues), backup, nil
}
