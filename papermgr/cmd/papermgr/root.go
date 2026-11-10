package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"papermgr/internal/config"
	"papermgr/internal/store"
	"papermgr/internal/tui"
)

var (
	flagJSON    bool
	flagLibrary string
)

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "papermgr",
		Short:         "Tag-centric, PDF-first paper manager",
		Long:          "PaperManage keeps every paper as a directory of plain files. The TUI is the main interface; the CLI is a first-class citizen.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().BoolVar(&flagJSON, "json", false, "emit structured JSON")
	root.PersistentFlags().StringVar(&flagLibrary, "library", "", "library root (default: global config / $PAPERMGR_LIBRARY)")
	root.AddCommand(
		newInitCmd(), newAddCmd(), newSearchCmd(), newListCmd(), newShowCmd(),
		newMetaCmd(), newTagCmd(), newAttachCmd(), newNoteCmd(), newOpenCmd(),
		newConvertCmd(), newRekeyCmd(), newRenameCmd(), newRmCmd(), newRestoreCmd(),
		newPurgeCmd(), newReindexCmd(), newBibCmd(), newPluginCmd(), newQueryCmd(), newMigrateCmd(), newTUICmd(),
		newVersionCmd(),
	)
	return root
}

func resolveLibrary() (string, error) {
	if flagLibrary != "" {
		return flagLibrary, nil
	}
	if env := os.Getenv("PAPERMGR_LIBRARY"); env != "" {
		return env, nil
	}
	g, err := config.LoadGlobal()
	if err == nil && g.Library != "" {
		return g.Library, nil
	}
	if err != nil {
		return "", err
	}
	return "", errors.New("no library configured; run `papermgr init <dir>` or set PAPERMGR_LIBRARY")
}

func loadGlobal() config.Global {
	g, _ := config.LoadGlobal()
	if g.LogDir == "" {
		g.LogDir = filepath.Join(config.StateDir(), "log")
	}
	return g
}

func openStore() (*store.Store, error) {
	root, err := resolveLibrary()
	if err != nil {
		return nil, codeErr(2, err)
	}
	s, err := store.Open(root, loadGlobal())
	if err != nil {
		if errors.Is(err, store.ErrLocked) {
			return nil, codeErr(3, err)
		}
		return nil, err
	}
	return s, nil
}

func withStore(fn func(*store.Store) error) error {
	s, err := openStore()
	if err != nil {
		return err
	}
	defer s.Close()
	return fn(s)
}

func printJSON(v interface{}) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func progress(done, total int) {
	if total == 0 {
		return
	}
	fmt.Fprintf(os.Stderr, "\rindexing %d/%d", done, total)
	if done == total {
		fmt.Fprintln(os.Stderr)
	}
}

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init [dir]",
		Short: "Create a new library",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			g := loadGlobal()
			root := g.Library
			if len(args) == 1 {
				root = args[0]
			}
			if root == "" {
				home, _ := os.UserHomeDir()
				root = filepath.Join(home, "papers")
			}
			abs, err := filepath.Abs(root)
			if err != nil {
				return err
			}
			s, err := store.Init(abs, g)
			if err != nil {
				return err
			}
			defer s.Close()
			if flagJSON {
				return printJSON(map[string]string{"library": abs})
			}
			fmt.Printf("initialised library at %s\n", abs)
			return nil
		},
	}
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version",
		RunE: func(cmd *cobra.Command, args []string) error {
			if flagJSON {
				return printJSON(map[string]string{"version": Version})
			}
			fmt.Println("papermgr", Version)
			return nil
		},
	}
}

// Version is set at build time.
var Version = "0.1.0-dev"

func newTUICmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Open the three-pane terminal interface",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore()
			if err != nil {
				return err
			}
			defer s.Close()
			if err := s.EnsureIndex(progress); err != nil {
				return err
			}
			return tui.Run(s)
		},
	}
}

func splitArgs(s string) []string { return strings.Fields(s) }
