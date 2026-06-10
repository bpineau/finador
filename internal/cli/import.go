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
		Use:   "import <fichier.csv>",
		Short: "Importe des transactions (colonnes par en-tête ; ré-import sans doublon)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			file, err := os.Open(args[0])
			if err != nil {
				return err
			}
			defer file.Close()
			var added, skipped int
			// mutate n'écrit le fichier que si tout l'import a réussi.
			if err := a.mutate(func(b *domain.Book) error {
				added, skipped, err = portfolio.ImportCSV(b, file)
				return err
			}); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%d importée(s), %d ignorée(s) (doublons)\n", added, skipped)
			return nil
		},
	}
}
