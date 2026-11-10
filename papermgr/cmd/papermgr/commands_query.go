package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"papermgr/internal/store"
)

func newQueryCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "query", Short: "Saved query files"}
	list := &cobra.Command{
		Use:   "list",
		Short: "List saved queries",
		RunE: func(cmd *cobra.Command, args []string) error {
			return withStore(func(s *store.Store) error {
				qs, err := s.ListQueries()
				if err != nil {
					return err
				}
				if flagJSON {
					return printJSON(qs)
				}
				for _, q := range qs {
					fmt.Printf("%-24s %s\n", q.Name, q.Expression)
				}
				return nil
			})
		},
	}
	save := &cobra.Command{
		Use:   "save <name> <query>",
		Short: "Save a query to queries/<name>.query",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withStore(func(s *store.Store) error {
				expr := strings.Join(args[1:], " ")
				if err := s.SaveQuery(args[0], expr); err != nil {
					return err
				}
				if flagJSON {
					return printJSON(map[string]string{"name": args[0], "expression": expr})
				}
				fmt.Printf("saved query %s\n", args[0])
				return nil
			})
		},
	}
	rm := &cobra.Command{
		Use:   "rm <name>",
		Short: "Delete a saved query",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withStore(func(s *store.Store) error {
				if err := s.DeleteQuery(args[0]); err != nil {
					return err
				}
				fmt.Println("removed", args[0])
				return nil
			})
		},
	}
	run := &cobra.Command{
		Use:   "run <name>",
		Short: "Run a saved query",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withStore(func(s *store.Store) error {
				expr, err := s.LoadQuery(args[0])
				if err != nil {
					return err
				}
				hits, err := searchHits(s, expr)
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
	cmd.AddCommand(list, save, rm, run)
	return cmd
}
