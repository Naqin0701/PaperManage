// Package store is the single write path for a PaperManage library.
// Files are the source of truth; the SQLite index is derived and disposable.
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"papermgr/internal/config"
	"papermgr/internal/index"
	"papermgr/internal/logging"
	"papermgr/internal/model"
	"papermgr/internal/plugin"
)

// ErrLocked is returned when another process holds the library lock.
var ErrLocked = errors.New("library is locked by another papermgr instance")

// ErrNotLibrary indicates the directory is not an initialized library.
var ErrNotLibrary = errors.New("not a papermgr library (run `papermgr init <dir>`)")

// Store owns a library.
type Store struct {
	Root    string
	Cfg     config.Library
	Global  config.Global
	Log     *logging.Logger
	Index   *index.Index
	Plugins *plugin.Manager

	lock  *os.File
	mu    sync.Mutex
	bibMu sync.Mutex
}

// Init creates the library layout at root and writes a default config.
func Init(root string, global config.Global) (*Store, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	for _, d := range []string{
		abs,
		filepath.Join(abs, "papers"),
		filepath.Join(abs, "queries"),
		filepath.Join(abs, ".trash"),
		filepath.Join(abs, ".papermgr", "log"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	if _, err := os.Stat(filepath.Join(abs, "config.toml")); errors.Is(err, os.ErrNotExist) {
		if err := config.SaveLibrary(abs, config.DefaultLibrary()); err != nil {
			return nil, err
		}
	}
	// An explicit init makes this library the default for subsequent commands.
	global.Library = abs
	if global.LogDir == "" {
		global.LogDir = filepath.Join(config.StateDir(), "log")
	}
	_ = config.SaveGlobal(global)
	return Open(abs, global)
}

// Open locks and opens an existing library.
func Open(root string, global config.Global) (*Store, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("%w: %s", ErrNotLibrary, abs)
	}
	if _, err := os.Stat(filepath.Join(abs, "papers")); errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrNotLibrary, abs)
	}
	lock, err := acquireLock(filepath.Join(abs, ".papermgr", "lock"))
	if err != nil {
		return nil, err
	}
	cfg, err := config.LoadLibrary(abs)
	if err != nil {
		lock.Close()
		return nil, err
	}
	if global.LogDir == "" {
		global.LogDir = filepath.Join(config.StateDir(), "log")
	}
	log := logging.New(global.LogDir)
	idx, err := index.Open(abs, cfg.IndexSupplements)
	if err != nil {
		lock.Close()
		return nil, err
	}
	s := &Store{Root: abs, Cfg: cfg, Global: global, Log: log, Index: idx, lock: lock}
	s.Plugins = plugin.NewManager(abs, cfg, global, log)
	return s, nil
}

// Close releases the lock and database.
func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	var err error
	if s.Index != nil {
		err = s.Index.Close()
	}
	if s.lock != nil {
		_ = s.lock.Close()
		s.lock = nil
	}
	return err
}

// PapersDir returns <root>/papers.
func (s *Store) PapersDir() string { return filepath.Join(s.Root, "papers") }

// PaperDir returns the absolute directory for a citekey.
func (s *Store) PaperDir(citekey string) string { return filepath.Join(s.Root, "papers", citekey) }

// MetaPath returns the meta.json path for a citekey.
func (s *Store) MetaPath(citekey string) string {
	return filepath.Join(s.PaperDir(citekey), "meta.json")
}

// StatePath returns the state.json path for a citekey.
func (s *Store) StatePath(citekey string) string {
	return filepath.Join(s.PaperDir(citekey), ".papermgr", "state.json")
}

// Exists reports whether a paper directory exists.
func (s *Store) Exists(citekey string) bool {
	st, err := os.Stat(s.PaperDir(citekey))
	return err == nil && st.IsDir()
}

// Load reads meta.json and state.json for citekey.
func (s *Store) Load(citekey string) (*model.Paper, error) {
	dir := s.PaperDir(citekey)
	data, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return nil, fmt.Errorf("read meta for %s: %w", citekey, err)
	}
	var m model.Meta
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse meta for %s: %w", citekey, err)
	}
	p := &model.Paper{Meta: m, State: DefaultState(), Dir: dir}
	if stData, err := os.ReadFile(filepath.Join(dir, ".papermgr", "state.json")); err == nil {
		if err := json.Unmarshal(stData, &p.State); err != nil {
			return nil, fmt.Errorf("parse state for %s: %w", citekey, err)
		}
	}
	if p.State.Fields == nil {
		p.State.Fields = map[string]model.FieldState{}
	}
	if p.Meta.Tags == nil {
		p.Meta.Tags = []string{}
	}
	if p.Meta.Authors == nil {
		p.Meta.Authors = []model.Author{}
	}
	return p, nil
}

// DefaultState returns a fresh state.json value.
func DefaultState() model.State {
	return model.State{
		SchemaVersion: model.SchemaVersion,
		Fields:        map[string]model.FieldState{},
		Review:        model.ReviewState{Status: "done"},
	}
}

// SaveMeta atomically writes meta.json and updates updated_at. It also
// records a baseline snapshot so a later hand edit can be detected.
func (s *Store) SaveMeta(p *model.Paper) error {
	p.Meta.SchemaVersion = model.SchemaVersion
	p.Meta.UpdatedAt = time.Now().Format(time.RFC3339)
	sort.Strings(p.Meta.Tags)
	p.Meta.Tags = dedupe(p.Meta.Tags)
	b, err := json.MarshalIndent(&p.Meta, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if err := config.AtomicWrite(filepath.Join(p.Dir, "meta.json"), b, 0o644); err != nil {
		return err
	}
	p.State.MetaSHA256 = hashBytes(b)
	p.State.MetaSnapshot = append([]byte(nil), b...)
	return s.SaveState(p)
}

// SaveState atomically writes state.json.
func (s *Store) SaveState(p *model.Paper) error {
	p.State.SchemaVersion = model.SchemaVersion
	if p.State.Fields == nil {
		p.State.Fields = map[string]model.FieldState{}
	}
	b, err := json.MarshalIndent(&p.State, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return config.AtomicWrite(filepath.Join(p.Dir, ".papermgr", "state.json"), b, 0o644)
}

// Save writes both meta.json and state.json.
func (s *Store) Save(p *model.Paper) error { return s.SaveMeta(p) }

// AllCitekeys lists paper directories on disk.
func (s *Store) AllCitekeys() ([]string, error) {
	entries, err := os.ReadDir(s.PapersDir())
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// HashFile computes the sha256 of a file.
func HashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// FindBySHA searches the library for a PDF with the given sha256.
func (s *Store) FindBySHA(sha string) (*model.Paper, error) {
	keys, err := s.AllCitekeys()
	if err != nil {
		return nil, err
	}
	for _, k := range keys {
		p, err := s.Load(k)
		if err != nil {
			continue
		}
		if p.Meta.PDF.SHA256 == sha {
			return p, nil
		}
	}
	return nil, nil
}

// FindByDOI searches the library for a DOI.
func (s *Store) FindByDOI(doi string) (*model.Paper, error) {
	if doi == "" {
		return nil, nil
	}
	keys, err := s.AllCitekeys()
	if err != nil {
		return nil, err
	}
	want := strings.ToLower(normalizeDOI(doi))
	for _, k := range keys {
		p, err := s.Load(k)
		if err != nil {
			continue
		}
		if strings.ToLower(normalizeDOI(p.Meta.DOI)) == want {
			return p, nil
		}
	}
	return nil, nil
}

// Reload refreshes the in-memory config.
func (s *Store) Reload() error {
	cfg, err := config.LoadLibrary(s.Root)
	if err != nil {
		return err
	}
	s.Cfg = cfg
	s.Plugins = plugin.NewManager(s.Root, cfg, s.Global, s.Log)
	return nil
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func dedupe(in []string) []string {
	if len(in) == 0 {
		return in
	}
	out := in[:0]
	seen := map[string]bool{}
	for _, v := range in {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// WriteMetaFile is used by migrations/repairs.
func WriteMetaFile(path string, m model.Meta) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return config.AtomicWrite(path, append(b, '\n'), 0o644)
}
