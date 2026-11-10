package store

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"papermgr/internal/model"
)

// ValidateTag enforces FR-4.7.
func ValidateTag(tag string) error {
	if tag == "" {
		return errors.New("tag must not be empty")
	}
	if strings.HasPrefix(tag, "/") || strings.HasSuffix(tag, "/") {
		return fmt.Errorf("tag %q must not start or end with '/'", tag)
	}
	if strings.Contains(tag, "//") {
		return fmt.Errorf("tag %q contains an empty segment", tag)
	}
	for _, r := range tag {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			return fmt.Errorf("tag %q must not contain whitespace", tag)
		}
	}
	if strings.Contains(tag, "\\") {
		return fmt.Errorf("tag %q must not contain backslashes", tag)
	}
	return nil
}

// Tags returns the tag tree with counts.
func (s *Store) Tags() ([]model.TagCount, error) { return s.Index.TagCounts() }

// AddTags attaches tags to a paper.
func (s *Store) AddTags(citekey string, tags ...string) error {
	for _, t := range tags {
		if err := ValidateTag(t); err != nil {
			return err
		}
	}
	p, err := s.Load(citekey)
	if err != nil {
		return err
	}
	s.detectManualEdits(p)
	existing := map[string]bool{}
	for _, t := range p.Meta.Tags {
		existing[t] = true
	}
	for _, t := range tags {
		if !existing[t] {
			p.Meta.Tags = append(p.Meta.Tags, t)
			existing[t] = true
		}
	}
	if err := s.Save(p); err != nil {
		return err
	}
	s.Log.Info("tag.add", map[string]interface{}{"citekey": citekey, "tags": tags})
	if err := s.SyncPaper(citekey); err != nil {
		return err
	}
	s.autoBib()
	return nil
}

// RemoveTags detaches tags from a paper.
func (s *Store) RemoveTags(citekey string, tags ...string) error {
	p, err := s.Load(citekey)
	if err != nil {
		return err
	}
	s.detectManualEdits(p)
	rm := map[string]bool{}
	for _, t := range tags {
		rm[t] = true
	}
	var kept []string
	for _, t := range p.Meta.Tags {
		if !rm[t] {
			kept = append(kept, t)
		}
	}
	p.Meta.Tags = kept
	if err := s.Save(p); err != nil {
		return err
	}
	s.Log.Info("tag.rm", map[string]interface{}{"citekey": citekey, "tags": tags})
	if err := s.SyncPaper(citekey); err != nil {
		return err
	}
	s.autoBib()
	return nil
}

// TagChange is one tag rewrite.
type TagChange struct {
	Citekey string
	Old     string
	New     string
}

// RenameTag renames old to new and cascades descendants (FR-4.3).
func (s *Store) RenameTag(old, new string, dryRun bool) ([]TagChange, error) {
	if err := ValidateTag(old); err != nil {
		return nil, err
	}
	if err := ValidateTag(new); err != nil {
		return nil, err
	}
	if old == new {
		return nil, nil
	}
	if new == old+"/" || strings.HasPrefix(new, old+"/") {
		return nil, errors.New("cannot rename a tag into its own descendant")
	}
	return s.rewriteTags(func(tag string) (string, bool) {
		if tag == old {
			return new, true
		}
		if strings.HasPrefix(tag, old+"/") {
			return new + strings.TrimPrefix(tag, old), true
		}
		return tag, false
	}, dryRun, "tag.rename")
}

// MergeTags merges a into b, cascading children (FR-4.4).
func (s *Store) MergeTags(a, b string, dryRun bool) ([]TagChange, error) {
	if err := ValidateTag(a); err != nil {
		return nil, err
	}
	if err := ValidateTag(b); err != nil {
		return nil, err
	}
	if a == b {
		return nil, nil
	}
	return s.rewriteTags(func(tag string) (string, bool) {
		if tag == a {
			return b, true
		}
		if strings.HasPrefix(tag, a+"/") {
			return b + strings.TrimPrefix(tag, a), true
		}
		return tag, false
	}, dryRun, "tag.merge")
}

// DeleteTag removes a tag and its subtree bindings; papers are untouched.
func (s *Store) DeleteTag(tag string, dryRun bool) ([]TagChange, error) {
	if err := ValidateTag(tag); err != nil {
		return nil, err
	}
	return s.rewriteTags(func(t string) (string, bool) {
		if t == tag || strings.HasPrefix(t, tag+"/") {
			return "", true
		}
		return t, false
	}, dryRun, "tag.delete")
}

func (s *Store) rewriteTags(mapFn func(string) (string, bool), dryRun bool, event string) ([]TagChange, error) {
	keys, err := s.AllCitekeys()
	if err != nil {
		return nil, err
	}
	var changes []TagChange
	for _, ck := range keys {
		p, err := s.Load(ck)
		if err != nil {
			continue
		}
		s.detectManualEdits(p)
		changed := false
		var tags []string
		seen := map[string]bool{}
		for _, t := range p.Meta.Tags {
			nt, hit := mapFn(t)
			if hit {
				changed = true
				changes = append(changes, TagChange{Citekey: ck, Old: t, New: nt})
				if nt == "" {
					continue
				}
			} else {
				nt = t
			}
			if !seen[nt] {
				seen[nt] = true
				tags = append(tags, nt)
			}
		}
		if !changed {
			continue
		}
		p.Meta.Tags = tags
		if !dryRun {
			if err := s.Save(p); err != nil {
				return changes, err
			}
			if err := s.SyncPaper(ck); err != nil {
				return changes, err
			}
		}
	}
	if !dryRun {
		s.Log.Info(event, map[string]interface{}{"changes": len(changes), "at": time.Now().Format(time.RFC3339)})
		s.autoBib()
	}
	return changes, nil
}
