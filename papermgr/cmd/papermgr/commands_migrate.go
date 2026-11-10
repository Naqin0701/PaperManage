package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"papermgr/internal/store"
)

func newMigrateCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Upgrade old schema versions (backs up first)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return withStore(func(s *store.Store) error {
				issues, err := s.SchemaIssues()
				if err != nil {
					return err
				}
				if len(issues) == 0 {
					if flagJSON {
						return printJSON(map[string]int{"migrated": 0})
					}
					fmt.Println("all documents use schema version 1")
					return nil
				}
				if !yes {
					fmt.Printf("%d paper(s) need migration:\n", len(issues))
					for _, is := range issues {
						fmt.Printf("  %s (meta=%d state=%d)\n", is.Citekey, is.MetaVersion, is.StateVersion)
					}
					if !confirm("migrate now? (a backup is written to .trash)") {
						return codeErr(1, fmt.Errorf("aborted"))
					}
				}
				n, backup, err := s.Migrate()
				if err != nil {
					return err
				}
				if flagJSON {
					return printJSON(map[string]interface{}{"migrated": n, "backup": backup})
				}
				fmt.Printf("migrated %d paper(s); backup: %s\n", n, backup)
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "do not ask for confirmation")
	return cmd
}
