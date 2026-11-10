package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"papermgr/internal/model"
)

// RenameOp describes one citekey change.
type RenameOp struct {
	Old          string   `json:"old"`
	New          string   `json:"new"`
	Skipped      bool     `json:"skipped"`
	Reason       string   `json:"reason,omitempty"`
	ChangedFiles []string `json:"changed_files,omitempty"`
	Wikilinks    []string `json:"wikilinks,omitempty"`
}

// PlanRekey computes new citekeys without touching disk.
func (s *Store) PlanRekey(force bool) ([]RenameOp, error) {
	keys, err := s.AllCitekeys()
	if err != nil {
		return nil, err
	}
	reserved := map[string]bool{}
	for _, k := range keys {
		reserved[k] = true
	}
	var ops []RenameOp
	for _, ck := range keys {
		p, err := s.Load(ck)
		if err != nil {
			ops = append(ops, RenameOp{Old: ck, Skipped: true, Reason: err.Error()})
			continue
		}
		if p.State.CitekeyLocked && !force {
			ops = append(ops, RenameOp{Old: ck, New: ck, Skipped: true, Reason: "citekey_locked (use --force)"})
			continue
		}
		base := s.GenerateCitekey(p.Meta)
		taken := func(k string) bool {
			if k == ck {
				return false // the paper's own current directory is free
			}
			return reserved[k] || s.Exists(k)
		}
		newKey := base
		if taken(newKey) {
			words := titleWords(p.Meta.Title, s.Cfg.TitleWords+1)
			if len(words) > 0 {
				first := Slug(words[0])
				if first != "" && !strings.Contains(base, first) {
					cand := base + "-" + first
					if !taken(cand) {
						newKey = cand
					}
				}
			}
			if newKey == base {
				for i := 2; ; i++ {
					cand := fmt.Sprintf("%s-%d", base, i)
					if !taken(cand) {
						newKey = cand
						break
					}
				}
			}
		}
		if newKey == ck {
			ops = append(ops, RenameOp{Old: ck, New: ck, Skipped: true, Reason: "unchanged"})
			continue
		}
		reserved[newKey] = true
		ops = append(ops, RenameOp{Old: ck, New: newKey})
	}
	// Report relative-link and wikilink impact even for a dry run.
	mapping := map[string]string{}
	for _, op := range ops {
		if !op.Skipped && op.Old != op.New {
			mapping[op.Old] = op.New
		}
	}
	if len(mapping) > 0 {
		files, wikis := s.rewriteLinks(mapping, false)
		for i := range ops {
			if ops[i].Skipped {
				continue
			}
			ops[i].ChangedFiles = files
			ops[i].Wikilinks = wikis
		}
	}
	return ops, nil
}

// ApplyRekey performs the planned renames, rewrites relative links and updates
// the index. It must be called only after a reviewable dry-run.
func (s *Store) ApplyRekey(ops []RenameOp) ([]RenameOp, error) {
	var active []RenameOp
	for _, op := range ops {
		if op.Skipped || op.Old == op.New {
			continue
		}
		active = append(active, op)
	}
	if len(active) == 0 {
		return ops, nil
	}
	seen := map[string]bool{}
	for _, op := range active {
		if seen[op.New] {
			return ops, fmt.Errorf("rekey collision: two papers would become %q", op.New)
		}
		seen[op.New] = true
		if !s.Exists(op.Old) {
			return ops, fmt.Errorf("paper %q disappeared", op.Old)
		}
		if s.Exists(op.New) {
			return ops, fmt.Errorf("cannot rekey %q: target %q already exists", op.Old, op.New)
		}
	}
	// Two-phase rename makes A->B, B->A safe.
	temps := make([]string, len(active))
	for i, op := range active {
		tmp := filepath.Join(s.PapersDir(), fmt.Sprintf(".rekey-tmp-%d-%d", os.Getpid(), i))
		if err := os.Rename(s.PaperDir(op.Old), tmp); err != nil {
			return ops, err
		}
		temps[i] = tmp
	}
	for i, op := range active {
		if err := os.Rename(temps[i], s.PaperDir(op.New)); err != nil {
			// Best-effort rollback: undo completed renames and return the
			// remaining temporary directories to their original names.
			for j := 0; j < i; j++ {
				_ = os.Rename(s.PaperDir(active[j].New), s.PaperDir(active[j].Old))
			}
			for j := i; j < len(active); j++ {
				_ = os.Rename(temps[j], s.PaperDir(active[j].Old))
			}
			return ops, err
		}
	}
	mapping := map[string]string{}
	for _, op := range active {
		mapping[op.Old] = op.New
		p, err := s.Load(op.New)
		if err != nil {
			return ops, err
		}
		p.Dir = s.PaperDir(op.New)
		p.Meta.Citekey = op.New
		if err := s.SaveMeta(p); err != nil {
			return ops, err
		}
		_ = s.Index.RenameCitekey(op.Old, op.New)
		if err := s.SyncPaper(op.New); err != nil {
			return ops, err
		}
	}
	files, wikilinks := s.rewriteLinks(mapping, true)
	for i := range ops {
		if ops[i].Skipped {
			continue
		}
		ops[i].ChangedFiles = files
		ops[i].Wikilinks = wikilinks
	}
	s.autoBib()
	s.Log.Info("rekey.done", map[string]interface{}{"renamed": len(active), "files_rewritten": len(files), "wikilinks": len(wikilinks)})
	return ops, nil
}

// PreviewRename validates a manual rename and reports affected links without
// changing anything (INV-7: rekey/rename must be previewable first).
func (s *Store) PreviewRename(old, new string) (RenameOp, error) {
	op := RenameOp{Old: old, New: new}
	if Slug(new) != new || new == "" {
		return op, fmt.Errorf("invalid citekey %q: use lowercase letters, digits and single dashes", new)
	}
	if !s.Exists(old) {
		return op, fmt.Errorf("paper %q not found", old)
	}
	if s.Exists(new) {
		return op, fmt.Errorf("target citekey %q already exists", new)
	}
	files, wikis := s.rewriteLinks(map[string]string{old: new}, false)
	op.ChangedFiles = files
	op.Wikilinks = wikis
	return op, nil
}

// RenamePaper manually renames one paper and locks its citekey.
func (s *Store) RenamePaper(old, new string) (RenameOp, error) {
	op, err := s.PreviewRename(old, new)
	if err != nil {
		return op, err
	}
	p, err := s.Load(old)
	if err != nil {
		return op, err
	}
	s.detectManualEdits(p)
	if err := os.Rename(s.PaperDir(old), s.PaperDir(new)); err != nil {
		return op, err
	}
	p.Dir = s.PaperDir(new)
	p.Meta.Citekey = new
	p.State.CitekeyLocked = true
	if err := s.Save(p); err != nil {
		return op, err
	}
	_ = s.Index.RenameCitekey(old, new)
	if err := s.SyncPaper(new); err != nil {
		return op, err
	}
	files, wikilinks := s.rewriteLinks(map[string]string{old: new}, true)
	op.ChangedFiles = files
	op.Wikilinks = wikilinks
	s.autoBib()
	s.Log.Info("paper.rename", map[string]interface{}{"old": old, "new": new})
	return op, nil
}

var textExts = map[string]bool{
	".md": true, ".markdown": true, ".tex": true, ".typ": true, ".txt": true,
	".json": true, ".yaml": true, ".yml": true, ".html": true, ".htm": true,
	".rst": true, ".org": true, ".toml": true, ".query": true,
}

// rewriteLinks rewrites papers/<old> and ../<old>/ style references in text
// files. Wikilinks are reported but never modified (FR-3.7).
func (s *Store) rewriteLinks(mapping map[string]string, write bool) ([]string, []string) {
	if len(mapping) == 0 {
		return nil, nil
	}
	var changed []string
	wikiset := map[string]bool{}
	root := s.Root
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".trash" || name == ".papermgr" || name == ".git" || name == "library.sqlite" {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if !textExts[ext] || filepath.Base(path) == "library.bib" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil || len(data) > 4*1024*1024 {
			return nil
		}
		newData, wikis := rewriteText(string(data), mapping)
		for _, w := range wikis {
			wikiset[w] = true
		}
		if newData != string(data) {
			rel, _ := filepath.Rel(root, path)
			changed = append(changed, rel)
			if write {
				_ = writeFileAtomic(path, []byte(newData))
			}
		}
		return nil
	})
	var wikilinks []string
	for w := range wikiset {
		wikilinks = append(wikilinks, w)
	}
	sort.Strings(wikilinks)
	sort.Strings(changed)
	return changed, wikilinks
}

func rewriteText(src string, mapping map[string]string) (string, []string) {
	var out strings.Builder
	var wikis []string
	i := 0
	for i < len(src) {
		if i+1 < len(src) && src[i] == '[' && src[i+1] == '[' {
			end := strings.Index(src[i+2:], "]]")
			if end >= 0 {
				w := src[i : i+2+end+2]
				wikis = append(wikis, w)
				out.WriteString(w)
				i += 2 + end + 2
				continue
			}
		}
		// Find next wikilink or end.
		next := strings.Index(src[i:], "[[")
		var chunk string
		if next < 0 {
			chunk = src[i:]
			i = len(src)
		} else {
			chunk = src[i : i+next]
			i += next
		}
		keys := make([]string, 0, len(mapping))
		for old := range mapping {
			keys = append(keys, old)
		}
		sort.Strings(keys)
		for i, old := range keys {
			ph := fmt.Sprintf("\x00REKEY%d\x00", i)
			chunk = strings.ReplaceAll(chunk, "papers/"+old+"/", "papers/"+ph+"/")
			chunk = strings.ReplaceAll(chunk, "../"+old+"/", "../"+ph+"/")
			chunk = strings.ReplaceAll(chunk, "./"+old+"/", "./"+ph+"/")
			chunk = strings.ReplaceAll(chunk, `"`+old+`/`, `"`+ph+`/`)
			chunk = strings.ReplaceAll(chunk, "("+old+"/", "("+ph+"/")
		}
		for i, old := range keys {
			ph := fmt.Sprintf("\x00REKEY%d\x00", i)
			chunk = strings.ReplaceAll(chunk, ph, mapping[old])
		}
		out.WriteString(chunk)
	}
	return out.String(), wikis
}

func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	info, err := os.Stat(path)
	mode := os.FileMode(0o644)
	if err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.Chmod(name, mode); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// ApplyReviewCandidate is a small helper for TUI use.
func (s *Store) ApplyReviewCandidate(p *model.Paper) error {
	if len(p.State.Review.Candidates) == 0 {
		return errors.New("no candidates")
	}
	return s.ReviewAccept(p.Meta.Citekey, 0)
}

var _ = json.Marshal
var _ = regexp.MustCompile
