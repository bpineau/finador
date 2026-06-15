package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"finador/internal/domain"
	"finador/internal/portfolio"
)

func importCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "import <file.csv>",
		Short:   "Import transactions (columns by header; re-import is idempotent)",
		Example: "  finador import broker.csv",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			file, err := os.Open(args[0])
			if err != nil {
				return err
			}
			defer file.Close()
			var added, skipped int
			// mutate only writes the file if the whole import succeeded.
			if err := a.mutate(func(b *domain.Book) error {
				added, skipped, err = portfolio.ImportCSV(b, file)
				return err
			}); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%d imported, %d skipped (duplicates)\n", added, skipped)
			return nil
		},
	}
}
