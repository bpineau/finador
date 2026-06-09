package cli

import (
	"cmp"
	"fmt"
	"text/tabwriter"

	"github.com/shopspring/decimal"
	"github.com/spf13/cobra"

	"finador/internal/domain"
)

func assetCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{Use: "asset", Short: "Gère les actifs : titres cotés et biens"}
	cmd.AddCommand(assetAdd(a), assetSet(a), assetList(a))
	return cmd
}

func assetAdd(a *app) *cobra.Command {
	var kind, name, isin, ccy, group, id string
	var aliases []string
	cmd := &cobra.Command{
		Use:   "add <ticker|nom>",
		Short: "Déclare un actif : ticker Yahoo pour un titre, nom pour un bien",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			k, err := domain.ParseAssetKind(kind)
			if err != nil {
				return err
			}
			asset := &domain.Asset{
				Kind:     k,
				Name:     cmp.Or(name, args[0]),
				ISIN:     isin,
				Aliases:  aliases,
				Currency: domain.Currency(ccy),
				Group:    group,
			}
			if k == domain.Security {
				asset.Ticker = args[0]
			}
			asset.ID = domain.AssetID(cmp.Or(id, domain.Slugify(asset.Name)))
			return a.mutate(func(b *domain.Book) error {
				if err := b.AddAsset(asset); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Actif %s (%s) créé\n", asset.Name, asset.ID)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&kind, "kind", "security", "security ou property")
	cmd.Flags().StringVar(&name, "name", "", "nom (défaut : l'argument)")
	cmd.Flags().StringVar(&isin, "isin", "", "code ISIN")
	cmd.Flags().StringArrayVar(&aliases, "alias", nil, "alias supplémentaire (répétable)")
	cmd.Flags().StringVar(&ccy, "ccy", "EUR", "devise de cotation")
	cmd.Flags().StringVar(&group, "group", "", "poche hiérarchique, ex. actions/us/tech")
	cmd.Flags().StringVar(&id, "id", "", "identifiant (défaut : slug du nom)")
	return cmd
}

func assetSet(a *app) *cobra.Command {
	var at, account, ccy string
	cmd := &cobra.Command{
		Use:   "set <actif> <valeur>",
		Short: "Pose une estimation datée (biens, parts non cotées)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.mutate(func(b *domain.Book) error {
				asset, err := b.Asset(args[0])
				if err != nil {
					return err
				}
				value, err := decimal.NewFromString(args[1])
				if err != nil {
					return fmt.Errorf("valeur %q: %w", args[1], err)
				}
				date, err := dateOrToday(at)
				if err != nil {
					return err
				}
				acc, err := accountFor(b, account, asset)
				if err != nil {
					return err
				}
				tx := b.Add(domain.Transaction{
					Date: date, Account: acc.ID, Asset: asset.ID, Kind: domain.Statement,
					Amount: domain.Money{Amount: value, Currency: cmp.Or(domain.Currency(ccy), asset.Currency)},
				})
				fmt.Fprintf(cmd.OutOrStdout(), "[%d] %s = %s au %s\n", tx.ID, asset.Name, tx.Amount, tx.Date)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&at, "at", "", "date AAAA-MM-JJ (défaut : aujourd'hui)")
	cmd.Flags().StringVar(&account, "account", "", "enveloppe (nom ou id)")
	cmd.Flags().StringVar(&ccy, "ccy", "", "devise (défaut : celle de l'actif)")
	return cmd
}

func assetList(a *app) *cobra.Command {
	return &cobra.Command{
		Use:  "list",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, err := a.open()
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tTYPE\tNOM\tTICKER\tGROUPE\tDEVISE")
			for _, as := range f.Book.Assets {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", as.ID, as.Kind, as.Name, as.Ticker, as.Group, as.Currency)
			}
			return w.Flush()
		},
	}
}
