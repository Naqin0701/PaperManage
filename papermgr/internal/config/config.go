// Package config loads global and library configuration.
package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// PluginConfig is the library-level override for one plugin.
type PluginConfig struct {
	Enabled *bool                  `toml:"enabled,omitempty"`
	Config  map[string]interface{} `toml:"config,omitempty"`
}

// Library is the library-level config.toml.
type Library struct {
	CitekeyTemplate  string                  `toml:"citekey_template"`
	TitleWords       int                     `toml:"title_words"`
	IndexSupplements bool                    `toml:"index_supplements"`
	Plugins          map[string]PluginConfig `toml:"plugins,omitempty"`
}

// Global is ~/.config/papermgr/config.toml.
type Global struct {
	Library string `toml:"library"`
	Editor  string `toml:"editor"`
	Viewer  string `toml:"viewer"`
	LogDir  string `toml:"log_dir"`
}

// DefaultLibrary returns library defaults.
func DefaultLibrary() Library {
	return Library{
		CitekeyTemplate:  "{author}-{year}-{title}",
		TitleWords:       3,
		IndexSupplements: false,
		Plugins:          map[string]PluginConfig{},
	}
}

// ConfigDir returns ~/.config/papermgr (honouring XDG_CONFIG_HOME).
func ConfigDir() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "papermgr")
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".config", "papermgr")
	}
	return filepath.Join(h, ".config", "papermgr")
}

// StateDir returns ~/.local/state/papermgr (honouring XDG_STATE_HOME).
func StateDir() string {
	if x := os.Getenv("XDG_STATE_HOME"); x != "" {
		return filepath.Join(x, "papermgr")
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".local", "state", "papermgr")
	}
	return filepath.Join(h, ".local", "state", "papermgr")
}

// PluginsDir returns the global plugin directory.
func PluginsDir() string { return filepath.Join(ConfigDir(), "plugins") }

// LoadGlobal reads the global config, returning defaults when missing.
func LoadGlobal() (Global, error) {
	g := Global{Editor: os.Getenv("EDITOR"), Viewer: os.Getenv("PDF_VIEWER")}
	if g.Editor == "" {
		g.Editor = "vi"
	}
	if g.Viewer == "" {
		g.Viewer = "xdg-open"
	}
	path := filepath.Join(ConfigDir(), "config.toml")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return g, nil
	}
	if err != nil {
		return g, err
	}
	if err := toml.Unmarshal(data, &g); err != nil {
		return g, err
	}
	if g.Editor == "" {
		g.Editor = "vi"
	}
	if g.Viewer == "" {
		g.Viewer = "xdg-open"
	}
	if g.LogDir == "" {
		g.LogDir = filepath.Join(StateDir(), "log")
	}
	return g, nil
}

// SaveGlobal writes the global config.
func SaveGlobal(g Global) error {
	if err := os.MkdirAll(ConfigDir(), 0o755); err != nil {
		return err
	}
	return WriteTOML(filepath.Join(ConfigDir(), "config.toml"), g)
}

// LoadLibrary reads <root>/config.toml, filling defaults.
func LoadLibrary(root string) (Library, error) {
	l := DefaultLibrary()
	path := filepath.Join(root, "config.toml")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return l, nil
	}
	if err != nil {
		return l, err
	}
	if err := toml.Unmarshal(data, &l); err != nil {
		return l, err
	}
	if l.CitekeyTemplate == "" {
		l.CitekeyTemplate = "{author}-{year}-{title}"
	}
	if l.TitleWords <= 0 {
		l.TitleWords = 3
	}
	if l.Plugins == nil {
		l.Plugins = map[string]PluginConfig{}
	}
	return l, nil
}

// SaveLibrary writes <root>/config.toml.
func SaveLibrary(root string, l Library) error {
	return WriteTOML(filepath.Join(root, "config.toml"), l)
}

// WriteTOML atomically writes a TOML file.
func WriteTOML(path string, v interface{}) error {
	var b strings.Builder
	if err := toml.NewEncoder(&b).Encode(v); err != nil {
		return err
	}
	return AtomicWrite(path, []byte(b.String()), 0o644)
}

// AtomicWrite writes data to path via a temp file + rename.
func AtomicWrite(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
