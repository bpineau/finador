package cli

import (
	"github.com/spf13/cobra"

	"finador/internal/store"
)

func compactCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "compact",
		Short: "Rewrite the ledger, dropping superseded and deleted records",
		Long: "Rewrite the ledger file into a minimal form. Past edits and deletions " +
			"are stored as extra journal records (so syncing stays append-friendly); " +
			"compact discards those dead records. Rarely needed.",
		Example: "  finador compact",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// compact rewrites the ledger, so it must go through the write path
			// (pull a fresh base, rewrite, push) - not a read. Otherwise, in
			// GitHub mode the compacted copy would never be pushed and the next
			// pull would silently restore the un-compacted remote.
			if err := a.mutateFile(func(f *store.File) error {
				return f.Compact()
			}); err != nil {
				return err
			}
			cmd.Println("Ledger compacted.")
			return nil
		},
	}
}
