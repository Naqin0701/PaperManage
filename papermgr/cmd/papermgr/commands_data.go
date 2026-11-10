package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"papermgr/internal/model"
	"papermgr/internal/store"
)

func newAddCmd() *cobra.Command {
	var opts store.AddOptions
	cmd := &cobra.Command{
		Use:   "add <file.pdf>...",
		Short: "Ingest one or more PDFs",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withStore(func(s *store.Store) error {
				var results []store.AddResult
				var failures []string
				for _, path := range args {
					res, err := s.AddPDF(path, opts)
					if err != nil {
						failures = append(failures, fmt.Sprintf("%s: %v", path, err))
						continue
					}
					results = append(results, res)
				}
				if flagJSON {
					_ = printJSON(map[string]interface{}{"results": results, "failures": failures})
				} else {
					for _, r := range results {
						status := "added"
						if r.Duplicate {
							status = "duplicate"
						}
						if r.Replaced {
							status = "replaced"
						}
						fmt.Printf("%-8s %-34s %s (review: %s)\n", status, r.Citekey, r.Title, r.Review)
						for _, w := range r.Warnings {
							fmt.Println("  WARN", w)
						}
					}
					for _, f := range failures {
						fmt.Fprintln(os.Stderr, "ERROR", f)
					}
				}
				if len(failures) > 0 {
					if len(results) == 0 {
						return codeErr(1, fmt.Errorf("all %d file(s) failed", len(failures)))
					}
					return codeErr(1, fmt.Errorf("%d of %d file(s) failed", len(failures), len(results)+len(failures)))
				}
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&opts.Yes, "yes", false, "skip confirmations (batch import)")
	cmd.Flags().BoolVar(&opts.Replace, "replace", false, "replace paper.pdf for an existing duplicate")
	cmd.Flags().StringArrayVar(&opts.Tags, "tag", nil, "tag to attach (repeatable)")
	return cmd
}

func searchHits(s *store.Store, expr string) ([]model.SearchHit, error) {
	if err := s.EnsureIndex(progress); err != nil {
		return nil, err
	}
	return s.Index.Query(expr)
}

func printHits(hits []model.SearchHit) {
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "CITEKEY\tYEAR\tTITLE\tTAGS")
	for _, h := range hits {
		year := ""
		if h.Year != nil {
			year = fmt.Sprint(*h.Year)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", h.Citekey, year, truncate(h.Title, 72), strings.Join(h.Tags, ","))
	}
	w.Flush()
	fmt.Fprintf(os.Stderr, "%d result(s)\n", len(hits))
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func newSearchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "search <query>",
		Short: "Search the library",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return withStore(func(s *store.Store) error {
				hits, err := searchHits(s, strings.Join(args, " "))
				if err != nil {
					if errors.Is(err, os.ErrNotExist) {
						return codeErr(2, err)
					}
					return codeErr(2, err)
				}
				if flagJSON {
					return printJSON(hits)
				}
				printHits(hits)
				return nil
			})
		},
	}
}

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all papers",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return withStore(func(s *store.Store) error {
				hits, err := searchHits(s, "sort:added-")
				if err != nil {
					return err
				}
				if flagJSON {
					return printJSON(hits)
				}
				printHits(hits)
				return nil
			})
		},
	}
}

type showOutput struct {
	Paper       *model.Paper       `json:"paper"`
	Attachments []store.Attachment `json:"attachments"`
	HasNote     bool               `json:"has_note"`
}

func newShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <citekey>",
		Short: "Show paper details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withStore(func(s *store.Store) error {
				p, err := s.Load(args[0])
				if err != nil {
					return err
				}
				atts, _ := s.Attachments(args[0])
				out := showOutput{Paper: p, Attachments: atts, HasNote: hasNote(atts)}
				if flagJSON {
					return printJSON(out)
				}
				printShow(out)
				return nil
			})
		},
	}
}

func hasNote(atts []store.Attachment) bool {
	for _, a := range atts {
		if a.Kind == "note" {
			return true
		}
	}
	return false
}

func printShow(out showOutput) {
	p := out.Paper
	fmt.Printf("%s\n", p.Meta.Title)
	fmt.Printf("  citekey   %s\n", p.Meta.Citekey)
	fmt.Printf("  id        %s\n", p.Meta.ID)
	fmt.Printf("  type      %s\n", p.Meta.EntryType)
	if p.Meta.Year != nil {
		fmt.Printf("  year      %d\n", *p.Meta.Year)
	}
	if len(p.Meta.Authors) > 0 {
		var names []string
		for _, a := range p.Meta.Authors {
			names = append(names, strings.TrimSpace(a.Given+" "+a.Family))
		}
		fmt.Printf("  authors   %s\n", strings.Join(names, "; "))
	}
	if p.Meta.Venue != nil {
		fmt.Printf("  venue     %s\n", p.Meta.Venue.Name)
	}
	if p.Meta.DOI != "" {
		fmt.Printf("  doi       %s\n", p.Meta.DOI)
	}
	if p.Meta.ArxivID != "" {
		fmt.Printf("  arxiv     %s\n", p.Meta.ArxivID)
	}
	if p.Meta.URL != "" {
		fmt.Printf("  url       %s\n", p.Meta.URL)
	}
	fmt.Printf("  tags      %s\n", strings.Join(p.Meta.Tags, ", "))
	fmt.Printf("  PDF       %s (%d bytes)\n", p.Meta.PDF.OriginalName, p.Meta.PDF.Size)
	fmt.Printf("  added     %s\n", p.Meta.AddedAt)
	fmt.Printf("  review    %s", p.State.Review.Status)
	if p.State.Review.Reason != "" {
		fmt.Printf(" — %s", p.State.Review.Reason)
	}
	fmt.Println()
	if p.Meta.Abstract != "" {
		fmt.Printf("\nAbstract:\n%s\n", p.Meta.Abstract)
	}
	fmt.Println("\nAttachments:")
	if len(out.Attachments) == 0 {
		fmt.Println("  (none)")
	}
	for _, a := range out.Attachments {
		fmt.Printf("  %-12s %s\n", a.Kind, a.Rel)
	}
}

func newMetaCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "meta", Short: "Maintain metadata"}
	show := &cobra.Command{
		Use:   "show <citekey>",
		Short: "Show metadata with provenance",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withStore(func(s *store.Store) error {
				p, err := s.Load(args[0])
				if err != nil {
					return err
				}
				if flagJSON {
					return printJSON(p)
				}
				fmt.Printf("title       %s\n", p.Meta.Title)
				fmt.Printf("entry_type  %s\n", p.Meta.EntryType)
				if p.Meta.Year != nil {
					fmt.Printf("year        %d\n", *p.Meta.Year)
				}
				fmt.Printf("doi         %s\n", p.Meta.DOI)
				fmt.Printf("abstract    %s\n", truncate(p.Meta.Abstract, 120))
				fmt.Printf("tags        %s\n", strings.Join(p.Meta.Tags, ", "))
				fmt.Println("\nprovenance:")
				for field, fs := range p.State.Fields {
					lock := ""
					if fs.Locked {
						lock = " [locked]"
					}
					fmt.Printf("  %-12s %-10s %s%s\n", field, fs.Source, fs.At, lock)
				}
				return nil
			})
		},
	}
	set := &cobra.Command{
		Use:   "set <citekey> <field>=<value>...",
		Short: "Set metadata fields manually (locks them)",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withStore(func(s *store.Store) error {
				ck := args[0]
				var changed []string
				for _, kv := range args[1:] {
					i := strings.Index(kv, "=")
					if i <= 0 {
						return codeErr(2, fmt.Errorf("expected field=value, got %q", kv))
					}
					field, value := kv[:i], kv[i+1:]
					if err := s.MetaSet(ck, field, value); err != nil {
						return err
					}
					changed = append(changed, field)
				}
				if flagJSON {
					return printJSON(map[string]interface{}{"citekey": ck, "changed": changed})
				}
				fmt.Printf("updated %s: %s\n", ck, strings.Join(changed, ", "))
				return nil
			})
		},
	}
	var all, force, pending bool
	refresh := &cobra.Command{
		Use:   "refresh <citekey>|--all",
		Short: "Re-fetch metadata, preserving locked fields",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return withStore(func(s *store.Store) error {
				if all {
					results := s.MetaRefreshAll(force, pending)
					if flagJSON {
						return printJSON(results)
					}
					for _, r := range results {
						fmt.Printf("%-34s changed=%-22s status=%s\n", r.Citekey, strings.Join(r.Changed, ","), r.Status)
						for _, sk := range r.Skipped {
							fmt.Printf("  locked, skipped: %s\n", sk)
						}
						if r.Error != "" {
							fmt.Printf("  ERROR %s\n", r.Error)
						}
					}
					return nil
				}
				if len(args) != 1 {
					return codeErr(2, errors.New("usage: meta refresh <citekey> | --all"))
				}
				r, err := s.MetaRefresh(args[0], force)
				if err != nil {
					return err
				}
				if flagJSON {
					return printJSON(r)
				}
				fmt.Printf("refreshed %s: changed=%v status=%s source=%s\n", r.Citekey, r.Changed, r.Status, r.Source)
				if len(r.Skipped) > 0 {
					fmt.Printf("locked fields skipped: %s\n", strings.Join(r.Skipped, ", "))
				}
				return nil
			})
		},
	}
	refresh.Flags().BoolVar(&all, "all", false, "refresh every paper")
	refresh.Flags().BoolVar(&force, "force", false, "overwrite locked fields")
	refresh.Flags().BoolVar(&pending, "pending", false, "with --all, only pending review papers")
	cmd.AddCommand(show, set, refresh)
	return cmd
}
