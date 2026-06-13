package cli

import (
	"fmt"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"finador/internal/domain"
)

func labelCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "label",
		Short:   "Tag a position — an (account, asset) pair — with free-form names",
		Example: "  finador label add retraite --asset CW8 --account \"PEA Zephyr\"",
	}
	cmd.AddCommand(labelAdd(a), labelRm(a), labelList(a))
	return cmd
}

func labelAdd(a *app) *cobra.Command {
	var asset, account string
	cmd := &cobra.Command{
		Use:     "add <name> --asset <ref> --account <ref>",
		Short:   "Tag a position with a name label",
		Example: "  finador label add retraite --asset CW8 --account \"PEA Zephyr\"",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			return a.mutate(func(b *domain.Book) error {
				as, err := b.Asset(asset)
				if err != nil {
					return err
				}
				acc, err := b.Account(account)
				if err != nil {
					return err
				}
				l := &domain.Label{ID: domain.LabelID(domain.NewID()), Account: acc.ID, Asset: as.ID, Name: name}
				if err := b.AddLabel(l); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "[%s] label %q on %s / %s\n", l.ID, l.Name, acc.Name, as.Name)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&asset, "asset", "", "asset (name, ticker, ISIN, alias or id)")
	cmd.Flags().StringVar(&account, "account", "", "account (name, alias or id)")
	_ = cmd.MarkFlagRequired("asset")
	_ = cmd.MarkFlagRequired("account")
	return cmd
}

func labelRm(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "rm <id-prefix>",
		Short:   "Remove a label by id (or unique id prefix)",
		Example: "  finador label rm 7e3a1",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.mutate(func(b *domain.Book) error {
				l, err := b.ResolveLabel(args[0])
				if err != nil {
					return err
				}
				if err := b.RemoveLabel(l.ID); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Label %s (%q) deleted\n", l.ID, l.Name)
				return nil
			})
		},
	}
}

func labelList(a *app) *cobra.Command {
	var asset, account, name string
	cmd := &cobra.Command{
		Use:     "list [--asset <ref>] [--account <ref>] [--name <substr>]",
		Short:   "List label assignments, optionally filtered",
		Example: "  finador label list --account \"PEA Zephyr\"",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, err := a.open()
			if err != nil {
				return err
			}
			b := f.Book

			var accID domain.AccountID
			if account != "" {
				acc, err := b.Account(account)
				if err != nil {
					return err
				}
				accID = acc.ID
			}
			var assetID domain.AssetID
			if asset != "" {
				as, err := b.Asset(asset)
				if err != nil {
					return err
				}
				assetID = as.ID
			}
			needle := strings.ToLower(name)

			labels := slices.Clone(b.Labels)
			slices.SortStableFunc(labels, func(x, y *domain.Label) int {
				return strings.Compare(string(x.ID), string(y.ID))
			})
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tACCOUNT\tASSET\tLABEL")
			for _, l := range labels {
				if accID != "" && l.Account != accID ||
					assetID != "" && l.Asset != assetID ||
					needle != "" && !strings.Contains(strings.ToLower(l.Name), needle) {
					continue
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
					l.ID, txAccountName(b, l.Account), txAssetName(b, l.Asset), l.Name)
			}
			return w.Flush()
		},
	}
	cmd.Flags().StringVar(&asset, "asset", "", "filter by asset")
	cmd.Flags().StringVar(&account, "account", "", "filter by account")
	cmd.Flags().StringVar(&name, "name", "", "filter by name (case-insensitive substring)")
	return cmd
}
