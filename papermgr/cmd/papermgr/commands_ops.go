package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"papermgr/internal/plugin"
	"papermgr/internal/store"
)

func newTagCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "tag", Short: "Tag operations"}
	list := &cobra.Command{
		Use:   "list",
		Short: "List tags and counts",
		RunE: func(cmd *cobra.Command, args []string) error {
			return withStore(func(s *store.Store) error {
				if err := s.EnsureIndex(progress); err != nil {
					return err
				}
				tags, err := s.Tags()
				if err != nil {
					return err
				}
				if flagJSON {
					return printJSON(tags)
				}
				for _, t := range tags {
					depth := strings.Count(t.Tag, "/")
					label := strings.Repeat("  ", depth) + t.Tag[strings.LastIndex(t.Tag, "/")+1:]
					fmt.Printf("%-40s %4d", label, t.Count)
					if t.Explicit > 0 {
						fmt.Printf("  (%d direct)", t.Explicit)
					}
					fmt.Println()
				}
				return nil
			})
		},
	}
	add := &cobra.Command{
		Use:   "add <citekey> <tag>...",
		Short: "Attach tags to a paper",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withStore(func(s *store.Store) error {
				if err := s.AddTags(args[0], args[1:]...); err != nil {
					return err
				}
				if flagJSON {
					return printJSON(map[string]interface{}{"citekey": args[0], "added": args[1:]})
				}
				fmt.Printf("tagged %s with %s\n", args[0], strings.Join(args[1:], ", "))
				return nil
			})
		},
	}
	rm := &cobra.Command{
		Use:   "rm <citekey> <tag>...",
		Short: "Detach tags from a paper",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withStore(func(s *store.Store) error {
				if err := s.RemoveTags(args[0], args[1:]...); err != nil {
					return err
				}
				if flagJSON {
					return printJSON(map[string]interface{}{"citekey": args[0], "removed": args[1:]})
				}
				fmt.Printf("removed %s from %s\n", strings.Join(args[1:], ", "), args[0])
				return nil
			})
		},
	}
	var renameDry, mergeDry, deleteDry bool
	rename := &cobra.Command{
		Use:   "rename <old> <new>",
		Short: "Rename a tag and cascade descendants",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withStore(func(s *store.Store) error {
				changes, err := s.RenameTag(args[0], args[1], renameDry)
				if err != nil {
					return err
				}
				return reportTagChanges(changes, renameDry, args[0]+" → "+args[1])
			})
		},
	}
	rename.Flags().BoolVar(&renameDry, "dry-run", false, "show changes without writing")
	merge := &cobra.Command{
		Use:   "merge <a> <b>",
		Short: "Merge tag a into b",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withStore(func(s *store.Store) error {
				changes, err := s.MergeTags(args[0], args[1], mergeDry)
				if err != nil {
					return err
				}
				return reportTagChanges(changes, mergeDry, args[0]+" → "+args[1])
			})
		},
	}
	merge.Flags().BoolVar(&mergeDry, "dry-run", false, "show changes without writing")
	del := &cobra.Command{
		Use:   "delete <tag>",
		Short: "Unbind a tag and its descendants (papers are kept)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withStore(func(s *store.Store) error {
				changes, err := s.DeleteTag(args[0], deleteDry)
				if err != nil {
					return err
				}
				return reportTagChanges(changes, deleteDry, "delete "+args[0])
			})
		},
	}
	del.Flags().BoolVar(&deleteDry, "dry-run", false, "show changes without writing")
	cmd.AddCommand(list, add, rm, rename, merge, del)
	return cmd
}

func reportTagChanges(changes []store.TagChange, dry bool, label string) error {
	if flagJSON {
		return printJSON(map[string]interface{}{"dry_run": dry, "operation": label, "changes": changes})
	}
	prefix := ""
	if dry {
		prefix = "[dry-run] "
	}
	fmt.Printf("%s%s: %d binding(s)\n", prefix, label, len(changes))
	for _, c := range changes {
		if c.New == "" {
			fmt.Printf("  %s: -%s\n", c.Citekey, c.Old)
		} else {
			fmt.Printf("  %s: %s → %s\n", c.Citekey, c.Old, c.New)
		}
	}
	return nil
}

func newAttachCmd() *cobra.Command {
	var kind, webURL string
	cmd := &cobra.Command{
		Use:   "attach <citekey> <file>...",
		Short: "Add attachments",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withStore(func(s *store.Store) error {
				ck := args[0]
				if webURL != "" {
					if len(args) != 1 {
						return codeErr(2, errors.New("--web takes only a citekey and a URL"))
					}
					files, pluginName, err := s.AttachWebURL(ck, webURL)
					if err != nil {
						return codeErr(4, err)
					}
					if flagJSON {
						return printJSON(map[string]interface{}{"citekey": ck, "files": files, "plugin": pluginName, "url": webURL})
					}
					fmt.Printf("captured %s into %s (plugin %s)\n", webURL, ck, pluginName)
					return nil
				}
				if len(args) < 2 {
					return codeErr(2, errors.New("usage: attach <citekey> <file>... [--kind note|supplement|web|misc]"))
				}
				att, err := s.Attach(ck, args[1:], kind)
				if err != nil {
					return err
				}
				if flagJSON {
					return printJSON(map[string]interface{}{"citekey": ck, "attachments": att})
				}
				for _, a := range att {
					fmt.Printf("attached %s (%s)\n", a.Rel, a.Kind)
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&kind, "kind", "", "note|supplement|web|misc (default: infer from extension)")
	cmd.Flags().StringVar(&webURL, "web", "", "snapshot a URL with a capture plugin")
	return cmd
}

func newNoteCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "note", Short: "Main note operations"}
	var format string
	var replace bool
	newCmd := &cobra.Command{
		Use:   "new <citekey>",
		Short: "Create the single main note",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withStore(func(s *store.Store) error {
				path, err := s.NoteNew(args[0], format, replace)
				if err != nil {
					return err
				}
				if flagJSON {
					return printJSON(map[string]string{"citekey": args[0], "path": path})
				}
				fmt.Println(path)
				return nil
			})
		},
	}
	newCmd.Flags().StringVar(&format, "format", "md", "md|tex|typ")
	newCmd.Flags().BoolVar(&replace, "replace", false, "overwrite an existing note")
	cmd.AddCommand(newCmd)
	return cmd
}

func newOpenCmd() *cobra.Command {
	var pdf, note bool
	var supplement, web string
	cmd := &cobra.Command{
		Use:   "open <citekey>",
		Short: "Open a paper, note or attachment with an external program",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withStore(func(s *store.Store) error {
				mode := ""
				name := ""
				switch {
				case note:
					mode = "note"
				case supplement != "":
					mode, name = "supplement", supplement
				case web != "":
					mode, name = "web", web
				case pdf:
					mode = "pdf"
				}
				path, err := s.OpenTarget(args[0], mode, name)
				if err != nil {
					return err
				}
				if flagJSON {
					return printJSON(map[string]string{"mode": modeOrDefault(mode), "path": path})
				}
				program := s.Global.Viewer
				if mode == "note" {
					program = s.Global.Editor
				}
				return runProgram(program, path)
			})
		},
	}
	cmd.Flags().BoolVar(&pdf, "pdf", false, "open paper.pdf (default)")
	cmd.Flags().BoolVar(&note, "note", false, "open the main note in $EDITOR")
	cmd.Flags().StringVar(&supplement, "supplement", "", "open a named supplement")
	cmd.Flags().StringVar(&web, "web", "", "open a named web snapshot")
	return cmd
}

func modeOrDefault(mode string) string {
	if mode == "" {
		return "pdf"
	}
	return mode
}

func runProgram(program, path string) error {
	parts := splitArgs(program)
	if len(parts) == 0 {
		return errors.New("no external program configured (set editor/viewer in ~/.config/papermgr/config.toml)")
	}
	args := parts[1:]
	found := false
	for i, a := range args {
		if strings.Contains(a, "%s") {
			args[i] = strings.ReplaceAll(a, "%s", path)
			found = true
		}
	}
	if !found {
		args = append(args, path)
	}
	c := exec.Command(parts[0], args...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return c.Run()
}

func newConvertCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "convert <citekey>",
		Short: "Convert PDF to Markdown with a convert plugin",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withStore(func(s *store.Store) error {
				files, pluginName, err := s.Convert(args[0])
				if err != nil {
					return codeErr(4, err)
				}
				if flagJSON {
					return printJSON(map[string]interface{}{"citekey": args[0], "files": files, "plugin": pluginName})
				}
				fmt.Printf("converted %s using %s (%d file(s))\n", args[0], pluginName, len(files))
				for _, f := range files {
					fmt.Println(" ", f)
				}
				return nil
			})
		},
	}
}

func newRekeyCmd() *cobra.Command {
	var dry, force, yes bool
	cmd := &cobra.Command{
		Use:   "rekey",
		Short: "Recompute citekeys from the template",
		RunE: func(cmd *cobra.Command, args []string) error {
			return withStore(func(s *store.Store) error {
				ops, err := s.PlanRekey(force)
				if err != nil {
					return err
				}
				if dry {
					return printRekeyPlan(ops, true)
				}
				if flagJSON {
					applied, err := s.ApplyRekey(ops)
					if err != nil {
						return err
					}
					return printJSON(applied)
				}
				var active int
				for _, op := range ops {
					if !op.Skipped && op.Old != op.New {
						active++
					}
				}
				if active == 0 {
					fmt.Println("nothing to rekey")
					return nil
				}
				if !yes {
					printRekeyPlanText(ops, true)
					if !confirm("apply these renames?") {
						return errors.New("aborted")
					}
				}
				applied, err := s.ApplyRekey(ops)
				if err != nil {
					return err
				}
				printRekeyPlanText(applied, false)
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&dry, "dry-run", false, "show mapping without writing")
	cmd.Flags().BoolVar(&force, "force", false, "also rekey locked papers")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip confirmation")
	return cmd
}

func printRekeyPlan(ops []store.RenameOp, dry bool) error {
	if flagJSON {
		return printJSON(map[string]interface{}{"dry_run": dry, "operations": ops})
	}
	printRekeyPlanText(ops, dry)
	return nil
}

func printRekeyPlanText(ops []store.RenameOp, dry bool) {
	if dry {
		fmt.Println("[dry-run] old → new")
	} else {
		fmt.Println("renamed:")
	}
	for _, op := range ops {
		if op.Skipped {
			if op.Reason != "unchanged" {
				fmt.Printf("  %-40s skipped: %s\n", op.Old, op.Reason)
			}
			continue
		}
		fmt.Printf("  %-40s → %s\n", op.Old, op.New)
		for _, f := range op.ChangedFiles {
			fmt.Printf("      rewrote %s\n", f)
		}
		for _, w := range op.Wikilinks {
			fmt.Printf("      needs manual check (wikilink): %s\n", w)
		}
	}
}

func newRenameCmd() *cobra.Command {
	var dry, yes bool
	cmd := &cobra.Command{
		Use:   "rename <old> <new>",
		Short: "Manually rename a citekey and lock it",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withStore(func(s *store.Store) error {
				preview, err := s.PreviewRename(args[0], args[1])
				if err != nil {
					return err
				}
				if dry {
					if flagJSON {
						return printJSON(map[string]interface{}{"dry_run": true, "operation": preview})
					}
					printRenamePreview(preview, true)
					return nil
				}
				if !yes && !flagJSON {
					printRenamePreview(preview, true)
					if !confirm("apply this rename?") {
						return errors.New("aborted")
					}
				}
				op, err := s.RenamePaper(args[0], args[1])
				if err != nil {
					return err
				}
				if flagJSON {
					return printJSON(op)
				}
				fmt.Printf("renamed %s → %s (citekey locked)\n", op.Old, op.New)
				for _, f := range op.ChangedFiles {
					fmt.Printf("  rewrote %s\n", f)
				}
				for _, w := range op.Wikilinks {
					fmt.Printf("  manual check: %s\n", w)
				}
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&dry, "dry-run", false, "show affected links without writing")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip confirmation")
	return cmd
}

func printRenamePreview(op store.RenameOp, dry bool) {
	prefix := ""
	if dry {
		prefix = "[dry-run] "
	}
	fmt.Printf("%s%s → %s\n", prefix, op.Old, op.New)
	for _, f := range op.ChangedFiles {
		fmt.Printf("  would rewrite %s\n", f)
	}
	for _, w := range op.Wikilinks {
		fmt.Printf("  needs manual check (wikilink): %s\n", w)
	}
}

func newRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <citekey>",
		Short: "Move a paper to .trash",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withStore(func(s *store.Store) error {
				path, err := s.Rm(args[0])
				if err != nil {
					return err
				}
				if flagJSON {
					return printJSON(map[string]string{"trash": path})
				}
				fmt.Printf("moved to %s\n", path)
				return nil
			})
		},
	}
}

func newRestoreCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restore <trash-entry>",
		Short: "Restore a paper from .trash",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withStore(func(s *store.Store) error {
				ck, err := s.Restore(args[0])
				if err != nil {
					return err
				}
				if flagJSON {
					return printJSON(map[string]string{"citekey": ck})
				}
				fmt.Printf("restored %s\n", ck)
				return nil
			})
		},
	}
}

func newPurgeCmd() *cobra.Command {
	var older, yes bool
	var olderThan string
	cmd := &cobra.Command{
		Use:   "purge",
		Short: "Permanently delete trash entries",
		RunE: func(cmd *cobra.Command, args []string) error {
			return withStore(func(s *store.Store) error {
				var age time.Duration
				if olderThan != "" {
					d, err := parseAge(olderThan)
					if err != nil {
						return codeErr(2, err)
					}
					age = d
					older = true
				}
				if !older && !yes {
					fmt.Print("permanently delete ALL trash entries? [y/N] ")
					if !confirm("") {
						return errors.New("aborted")
					}
				}
				n, err := s.Purge(age)
				if err != nil {
					return err
				}
				if flagJSON {
					return printJSON(map[string]interface{}{"purged": n})
				}
				fmt.Printf("purged %d trash entr(ies)\n", n)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&olderThan, "older-than", "", "only purge entries older than e.g. 30d")
	cmd.Flags().BoolVar(&yes, "yes", false, "do not ask for confirmation")
	return cmd
}

func parseAge(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil {
			return 0, fmt.Errorf("invalid age %q", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	if strings.HasSuffix(s, "w") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "w"))
		if err != nil {
			return 0, fmt.Errorf("invalid age %q", s)
		}
		return time.Duration(n) * 7 * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

func confirm(prompt string) bool {
	if prompt != "" {
		fmt.Printf("%s [y/N] ", prompt)
	}
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	return line == "y" || line == "yes"
}

func newReindexCmd() *cobra.Command {
	var rebuild bool
	cmd := &cobra.Command{
		Use:   "reindex",
		Short: "Synchronise the search index",
		RunE: func(cmd *cobra.Command, args []string) error {
			return withStore(func(s *store.Store) error {
				stats, err := s.Reindex(rebuild, progress)
				if err != nil {
					return err
				}
				if flagJSON {
					return printJSON(stats)
				}
				fmt.Printf("synced %d, unchanged %d, removed %d, errors %d (%.2fs)\n",
					stats.Synced, stats.Unchanged, stats.Removed, len(stats.Errors), stats.Elapsed.Seconds())
				for _, e := range stats.Errors {
					fmt.Fprintln(os.Stderr, "WARN", e)
				}
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&rebuild, "rebuild", false, "drop and rebuild all index tables")
	return cmd
}

func newBibCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "bib", Short: "BibTeX operations"}
	var dry bool
	export := &cobra.Command{
		Use:   "export",
		Short: "Regenerate library.bib (one-way export only)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return withStore(func(s *store.Store) error {
				if dry {
					content, err := s.BibContent()
					if err != nil {
						return err
					}
					fmt.Print(content)
					return nil
				}
				if err := s.ExportBib(); err != nil {
					return err
				}
				if flagJSON {
					return printJSON(map[string]string{"path": s.Root + "/library.bib"})
				}
				fmt.Println(s.Root + "/library.bib")
				return nil
			})
		},
	}
	export.Flags().BoolVar(&dry, "dry-run", false, "print to stdout instead of writing")
	cmd.AddCommand(export)
	return cmd
}

func newPluginCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "plugin", Short: "Plugin management"}
	var noHealth bool
	list := &cobra.Command{
		Use:   "list",
		Short: "List installed plugins",
		RunE: func(cmd *cobra.Command, args []string) error {
			return withStore(func(s *store.Store) error {
				infos := s.Plugins.List(!noHealth)
				if flagJSON {
					return printJSON(infos)
				}
				if len(infos) == 0 {
					fmt.Println("no plugins installed (see ~/.config/papermgr/plugins)")
					return nil
				}
				for _, p := range infos {
					state := "ok"
					if !p.Enabled {
						state = "disabled"
					} else if !p.Healthy {
						state = "unhealthy"
					}
					missing := ""
					if len(p.Missing) > 0 {
						missing = " missing=" + strings.Join(p.Missing, ",")
					}
					errStr := ""
					if p.Error != "" {
						errStr = " error=" + p.Error
					}
					fmt.Printf("%-16s %-8s prio=%-5d caps=%-28s %s%s%s\n",
						p.Name, p.Version, p.Priority, strings.Join(p.Capabilities, ","), state, missing, errStr)
				}
				return nil
			})
		},
	}
	list.Flags().BoolVar(&noHealth, "no-health", false, "skip health checks")
	var link bool
	install := &cobra.Command{
		Use:   "install <dir>",
		Short: "Install a plugin into the global plugin directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var dst string
			var err error
			if link {
				dst, err = plugin.Link(args[0])
			} else {
				dst, err = plugin.Install(args[0])
			}
			if err != nil {
				return err
			}
			if flagJSON {
				return printJSON(map[string]string{"installed": dst})
			}
			fmt.Println("installed", dst)
			return nil
		},
	}
	install.Flags().BoolVar(&link, "link", false, "symlink instead of copy")
	enable := &cobra.Command{
		Use:   "enable <name>",
		Short: "Enable a plugin for this library",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return setPluginEnabled(args[0], true)
		},
	}
	disable := &cobra.Command{
		Use:   "disable <name>",
		Short: "Disable a plugin for this library",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return setPluginEnabled(args[0], false)
		},
	}
	cmd.AddCommand(list, install, enable, disable)
	return cmd
}

func setPluginEnabled(name string, enabled bool) error {
	return withStore(func(s *store.Store) error {
		if err := plugin.SetEnabled(s.Root, name, enabled); err != nil {
			return err
		}
		if flagJSON {
			return printJSON(map[string]interface{}{"plugin": name, "enabled": enabled})
		}
		state := "disabled"
		if enabled {
			state = "enabled"
		}
		fmt.Printf("%s %s for this library\n", state, name)
		return nil
	})
}

var _ = json.Marshal
