package cli

import (
	"github.com/spf13/cobra"
)

func compactCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "compact",
		Short: "Rewrite the ledger, dropping superseded and deleted records",
		Long: "Rewrite the ledger file into a minimal form. Past edits and deletions " +
			"are stored as extra journal records (so syncing stays append-friendly); " +
			"compact discards those dead records. Rarely needed.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, err := a.open()
			if err != nil {
				return err
			}
			if err := f.Compact(); err != nil {
				return err
			}
			cmd.Println("Ledger compacted.")
			return nil
		},
	}
}
