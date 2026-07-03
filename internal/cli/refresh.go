package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"finador/internal/market"
)

func refreshCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "refresh",
		Short:   "Refresh quotes, exchange rates and dividends from Yahoo",
		Example: "  finador refresh",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if a.offline {
				return errors.New("refresh unavailable in --offline mode")
			}
			f, err := a.open()
			if err != nil {
				return err
			}
			sum := market.Refresh(cmd.Context(), f.Book, a.marketSource(), true)
			for _, w := range sum.Warnings {
				fmt.Fprintln(cmd.ErrOrStderr(), "warning:", w)
			}
			// An explicit refresh means "give me the market now": always
			// finish with a spot pass for today's live prices.
			spot := market.SpotRefresh(cmd.Context(), f.Book, a.marketSource())
			for _, w := range spot.Warnings {
				fmt.Fprintln(cmd.ErrOrStderr(), "warning:", w)
			}
			if err := f.SaveCache(); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%d series refreshed\n", len(sum.Fetched))
			return nil
		},
	}
}
