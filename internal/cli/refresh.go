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
			// An explicit refresh means "give me the market now": always
			// finish with a spot pass for today's live prices.
			spot := market.SpotRefresh(cmd.Context(), f.Book, a.marketSource())
			warn(cmd, sum.Warnings, spot.Warnings)
			if err := f.SaveCache(); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%d series refreshed, %d quotes\n",
				len(sum.Fetched), len(spot.Quotes))
			// The command the user reaches for when a figure looks wrong owes
			// them the answer to "is this price current?". Only here: a market
			// shut for the night would make every other command list this.
			for _, s := range spot.Stale {
				fmt.Fprintln(cmd.OutOrStdout(), "  stale:", s)
			}
			return nil
		},
	}
}
