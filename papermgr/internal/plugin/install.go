package plugin

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"

	"papermgr/internal/config"
)

// Install copies a plugin directory into the global plugin directory.
func Install(srcDir string) (string, error) {
	abs, err := filepath.Abs(srcDir)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(abs, "plugin.toml"))
	if err != nil {
		return "", fmt.Errorf("not a plugin directory: %w", err)
	}
	var mf Manifest
	if err := toml.Unmarshal(data, &mf); err != nil {
		return "", fmt.Errorf("invalid plugin.toml: %w", err)
	}
	if mf.Name == "" {
		return "", errors.New("plugin.toml missing name")
	}
	dst := filepath.Join(config.PluginsDir(), mf.Name)
	if _, err := os.Stat(dst); err == nil {
		return "", fmt.Errorf("plugin %q already installed at %s", mf.Name, dst)
	}
	if err := copyTree(abs, dst); err != nil {
		return "", err
	}
	return dst, nil
}

// Link symlinks a plugin directory instead of copying it.
func Link(srcDir string) (string, error) {
	abs, err := filepath.Abs(srcDir)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(abs, "plugin.toml"))
	if err != nil {
		return "", err
	}
	var mf Manifest
	if err := toml.Unmarshal(data, &mf); err != nil {
		return "", err
	}
	dst := filepath.Join(config.PluginsDir(), mf.Name)
	if err := os.MkdirAll(config.PluginsDir(), 0o755); err != nil {
		return "", err
	}
	if err := os.Symlink(abs, dst); err != nil {
		return "", err
	}
	return dst, nil
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		info, err := d.Info()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode())
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
}

// SetEnabled writes enabled state into the library config.
func SetEnabled(root, name string, enabled bool) error {
	cfg, err := config.LoadLibrary(root)
	if err != nil {
		return err
	}
	if cfg.Plugins == nil {
		cfg.Plugins = map[string]config.PluginConfig{}
	}
	pc := cfg.Plugins[name]
	pc.Enabled = &enabled
	cfg.Plugins[name] = pc
	return config.SaveLibrary(root, cfg)
}
