package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"papermgr/internal/model"
)

// TrashEntry is one deleted paper in .trash.
type TrashEntry struct {
	Name    string    `json:"name"`
	Citekey string    `json:"citekey"`
	Title   string    `json:"title"`
	Deleted time.Time `json:"deleted"`
	Path    string    `json:"path"`
}

// Rm moves a paper directory into .trash (INV-8).
func (s *Store) Rm(citekey string) (string, error) {
	if !s.Exists(citekey) {
		return "", fmt.Errorf("paper %q not found", citekey)
	}
	p, _ := s.Load(citekey)
	ts := time.Now().Format("20060102T150405")
	dst := filepath.Join(s.Root, ".trash", ts+"-"+citekey)
	for i := 2; fileExists(dst); i++ {
		dst = filepath.Join(s.Root, ".trash", fmt.Sprintf("%s-%s-%d", ts, citekey, i))
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(s.PaperDir(citekey), dst); err != nil {
		return "", err
	}
	if p != nil {
		_ = s.Index.DeleteByID(p.Meta.ID)
	} else {
		_ = s.Index.DeleteByCitekey(citekey)
	}
	s.autoBib()
	s.Log.Info("paper.rm", map[string]interface{}{"citekey": citekey, "trash": dst})
	return dst, nil
}

// Restore moves a trash entry back into papers/.
func (s *Store) Restore(name string) (string, error) {
	src := filepath.Join(s.Root, ".trash", filepath.Base(name))
	st, err := os.Stat(src)
	if err != nil || !st.IsDir() {
		return "", fmt.Errorf("trash entry %q not found", name)
	}
	data, err := os.ReadFile(filepath.Join(src, "meta.json"))
	if err != nil {
		return "", fmt.Errorf("trash entry has no meta.json: %w", err)
	}
	var m model.Meta
	if err := json.Unmarshal(data, &m); err != nil {
		return "", err
	}
	if m.Citekey == "" {
		return "", fmt.Errorf("trash entry meta.json has no citekey")
	}
	dst := s.PaperDir(m.Citekey)
	if fileExists(dst) {
		return "", fmt.Errorf("cannot restore: citekey %q already exists in papers/", m.Citekey)
	}
	if err := os.Rename(src, dst); err != nil {
		return "", err
	}
	if err := s.SyncPaper(m.Citekey); err != nil {
		return m.Citekey, err
	}
	s.autoBib()
	s.Log.Info("paper.restore", map[string]interface{}{"citekey": m.Citekey, "trash": src})
	return m.Citekey, nil
}

// ListTrash returns trash entries newest first.
func (s *Store) ListTrash() ([]TrashEntry, error) {
	dir := filepath.Join(s.Root, ".trash")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil
	}
	var out []TrashEntry
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), "backup-") {
			continue
		}
		info, _ := e.Info()
		te := TrashEntry{Name: e.Name(), Path: filepath.Join(dir, e.Name())}
		if data, err := os.ReadFile(filepath.Join(te.Path, "meta.json")); err == nil {
			var m model.Meta
			if json.Unmarshal(data, &m) == nil {
				te.Citekey = m.Citekey
				te.Title = m.Title
			}
		}
		if info != nil {
			te.Deleted = info.ModTime()
		}
		out = append(out, te)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// Purge permanently deletes trash entries. A zero age purges everything.
func (s *Store) Purge(olderThan time.Duration) (int, error) {
	entries, err := s.ListTrash()
	if err != nil {
		return 0, err
	}
	cutoff := time.Now().Add(-olderThan)
	n := 0
	for _, e := range entries {
		if olderThan > 0 && e.Deleted.After(cutoff) {
			continue
		}
		if err := os.RemoveAll(e.Path); err != nil {
			return n, err
		}
		n++
	}
	if n > 0 {
		s.Log.Info("trash.purge", map[string]interface{}{"count": n})
	}
	return n, nil
}
