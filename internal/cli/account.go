package cli

import (
	"cmp"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"finador/internal/domain"
)

func accountCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{Use: "account", Short: "Gère les enveloppes (PEA, CTO, PER, comptes bancaires…)"}
	cmd.AddCommand(accountAdd(a), accountList(a))
	return cmd
}

func accountAdd(a *app) *cobra.Command {
	var tax, ccy, id string
	cmd := &cobra.Command{
		Use:   "add <nom>",
		Short: "Crée une enveloppe — le nom est libre : \"PEA Zephyr\", \"CTO IBKR\"…",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rule, err := domain.ParseTaxRule(tax)
			if err != nil {
				return err
			}
			acc := &domain.Account{
				ID:       domain.AccountID(cmp.Or(id, domain.Slugify(args[0]))),
				Name:     args[0],
				Currency: domain.Currency(ccy),
				Tax:      rule,
			}
			return a.mutate(func(b *domain.Book) error {
				if err := b.AddAccount(acc); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Compte %s (%s) créé\n", acc.Name, acc.ID)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&tax, "tax", "none", "règle fiscale : none, gains:17.2%, value:20%")
	cmd.Flags().StringVar(&ccy, "ccy", "EUR", "devise du compte")
	cmd.Flags().StringVar(&id, "id", "", "identifiant (défaut : slug du nom)")
	return cmd
}

func accountList(a *app) *cobra.Command {
	return &cobra.Command{
		Use:  "list",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, err := a.open()
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tNOM\tDEVISE\tFISCALITÉ")
			for _, acc := range f.Book.Accounts {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", acc.ID, acc.Name, acc.Currency, acc.Tax)
			}
			return w.Flush()
		},
	}
}
