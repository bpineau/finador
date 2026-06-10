package cli

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"finador/internal/domain"
)

func accountCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{Use: "account", Short: "Manage accounts (PEA, CTO, PER, bank accounts…)"}
	cmd.AddCommand(accountAdd(a), accountList(a))
	return cmd
}

func accountAdd(a *app) *cobra.Command {
	var tax, ccy, id string
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Create an account — the name is free: \"PEA Zephyr\", \"CTO IBKR\"…",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rule, err := domain.ParseTaxRule(tax)
			if err != nil {
				return err
			}
			parsedCcy, err := domain.ParseCurrency(ccy)
			if err != nil {
				return err
			}
			accID := id
			if accID == "" {
				accID = domain.Slugify(args[0])
			}
			acc := &domain.Account{
				ID:       domain.AccountID(accID),
				Name:     args[0],
				Currency: parsedCcy,
				Tax:      rule,
			}
			return a.mutate(func(b *domain.Book) error {
				if err := b.AddAccount(acc); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Account %s (%s) created\n", acc.Name, acc.ID)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&tax, "tax", "none", "tax rule: none, gains:17.2%, value:20%")
	cmd.Flags().StringVar(&ccy, "ccy", "EUR", "account currency")
	cmd.Flags().StringVar(&id, "id", "", "identifier (default: slug of the name)")
	return cmd
}

func accountList(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List accounts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, err := a.open()
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tNAME\tCURRENCY\tTAX")
			for _, acc := range f.Book.Accounts {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", acc.ID, acc.Name, acc.Currency, acc.Tax)
			}
			return w.Flush()
		},
	}
}
