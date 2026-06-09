package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"finador/internal/market"
)

func refreshCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "refresh",
		Short: "Rafraîchit cours, change et dividendes depuis Yahoo",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if a.offline {
				return errors.New("refresh impossible en --offline")
			}
			f, err := a.open()
			if err != nil {
				return err
			}
			sum := market.Refresh(cmd.Context(), f.Book, a.marketSource(), true)
			for _, w := range sum.Warnings {
				fmt.Fprintln(cmd.ErrOrStderr(), "avertissement:", w)
			}
			if err := f.Save(); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%d série(s) rafraîchie(s)\n", len(sum.Fetched))
			return nil
		},
	}
}
