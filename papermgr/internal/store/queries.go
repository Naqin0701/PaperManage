package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"papermgr/internal/config"
)

// SavedQuery is a queries/<name>.query file.
type SavedQuery struct {
	Name       string `json:"name"`
	Expression string `json:"expression"`
	ModTime    string `json:"mod_time,omitempty"`
}

var queryNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// ValidateQueryName restricts saved query names to safe file names.
func ValidateQueryName(name string) error {
	if !queryNameRe.MatchString(name) {
		return fmt.Errorf("invalid query name %q (letters, digits, - and _ only)", name)
	}
	return nil
}

// SaveQuery writes queries/<name>.query.
func (s *Store) SaveQuery(name, expr string) error {
	if err := ValidateQueryName(name); err != nil {
		return err
	}
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return errors.New("query expression must not be empty")
	}
	dir := filepath.Join(s.Root, "queries")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := config.AtomicWrite(filepath.Join(dir, name+".query"), []byte(expr+"\n"), 0o644); err != nil {
		return err
	}
	s.Log.Info("query.save", map[string]interface{}{"name": name})
	return nil
}

// LoadQuery reads a saved query.
func (s *Store) LoadQuery(name string) (string, error) {
	if err := ValidateQueryName(name); err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(s.Root, "queries", name+".query"))
	if err != nil {
		return "", fmt.Errorf("saved query %q not found", name)
	}
	return strings.TrimSpace(string(data)), nil
}

// ListQueries lists saved queries sorted by name.
func (s *Store) ListQueries() ([]SavedQuery, error) {
	dir := filepath.Join(s.Root, "queries")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []SavedQuery
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".query") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		info, _ := e.Info()
		q := SavedQuery{Name: strings.TrimSuffix(e.Name(), ".query"), Expression: strings.TrimSpace(string(data))}
		if info != nil {
			q.ModTime = info.ModTime().Format("2006-01-02 15:04")
		}
		out = append(out, q)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// DeleteQuery removes a saved query file.
func (s *Store) DeleteQuery(name string) error {
	if err := ValidateQueryName(name); err != nil {
		return err
	}
	err := os.Remove(filepath.Join(s.Root, "queries", name+".query"))
	if os.IsNotExist(err) {
		return fmt.Errorf("saved query %q not found", name)
	}
	if err == nil {
		s.Log.Info("query.delete", map[string]interface{}{"name": name})
	}
	return err
}
