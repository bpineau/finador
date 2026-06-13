package cli

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"finador/internal/domain"
)

func accountCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{Use: "account", Short: "Manage accounts (PEA, CTO, PER, bank accounts…)"}
	cmd.AddCommand(accountAdd(a), accountList(a), accountEdit(a), accountRm(a))
	return cmd
}

func accountAdd(a *app) *cobra.Command {
	var tax, ccy string
	var aliases []string
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
			acc := &domain.Account{
				ID:       domain.AccountID(domain.NewID()),
				Name:     args[0],
				Currency: parsedCcy,
				Tax:      rule,
				Aliases:  aliases,
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
	cmd.Flags().StringArrayVar(&aliases, "alias", nil, "additional alias (repeatable)")
	return cmd
}

func accountRm(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "rm <account>",
		Short: "Delete an account with no transactions",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.mutate(func(b *domain.Book) error {
				if err := b.RemoveAccount(args[0]); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Account deleted\n")
				return nil
			})
		},
	}
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
			fmt.Fprintln(w, "ID\tNAME\tCURRENCY\tTAX\tALIASES")
			for _, acc := range f.Book.Accounts {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
					acc.ID, acc.Name, acc.Currency, acc.Tax,
					strings.Join(acc.Aliases, ","))
			}
			return w.Flush()
		},
	}
}

func accountEdit(a *app) *cobra.Command {
	var name, tax, ccy string
	var addAlias, rmAlias []string
	cmd := &cobra.Command{
		Use:   "edit <account>",
		Short: "Edit account fields passed as flags (name, tax, currency, aliases…)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.mutate(func(b *domain.Book) error {
				acc, err := b.Account(args[0])
				if err != nil {
					return err
				}
				if name != "" {
					acc.Name = name
				}
				if tax != "" {
					if acc.Tax, err = domain.ParseTaxRule(tax); err != nil {
						return err
					}
				}
				if ccy != "" {
					if acc.Currency, err = domain.ParseCurrency(ccy); err != nil {
						return err
					}
				}
				acc.Aliases = applyAliasEdits(acc.Aliases, addAlias, rmAlias)
				if err := b.CheckAccountRefs(acc); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Account %s updated\n", acc.ID)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new name")
	cmd.Flags().StringVar(&tax, "tax", "", "new tax rule: none, gains:17.2%, value:20%")
	cmd.Flags().StringVar(&ccy, "ccy", "", "new account currency")
	cmd.Flags().StringArrayVar(&addAlias, "add-alias", nil, "alias to add (repeatable)")
	cmd.Flags().StringArrayVar(&rmAlias, "rm-alias", nil, "alias to remove (repeatable)")
	return cmd
}
