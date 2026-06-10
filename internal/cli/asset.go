package cli

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/shopspring/decimal"
	"github.com/spf13/cobra"

	"finador/internal/domain"
)

func assetCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{Use: "asset", Short: "Gère les actifs : titres cotés et biens"}
	cmd.AddCommand(assetAdd(a), assetSet(a), assetList(a), assetEdit(a), assetRm(a))
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
			ccyParsed, err := domain.ParseCurrency(ccy)
			if err != nil {
				return err
			}
			asset := &domain.Asset{
				Kind:     k,
				Name:     cmp.Or(name, args[0]),
				ISIN:     isin,
				Aliases:  aliases,
				Currency: ccyParsed,
				Group:    group,
			}
			if k == domain.Security {
				asset.Ticker = args[0]
				if !a.offline {
					enrichFromMarket(cmd, a, asset, args[0],
						cmd.Flags().Changed("name"), cmd.Flags().Changed("ccy"))
				}
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
				effectiveCcy, err := currencyOr(ccy, asset.Currency)
				if err != nil {
					return err
				}
				tx := b.Add(domain.Transaction{
					Date: date, Account: acc.ID, Asset: asset.ID, Kind: domain.Statement,
					Amount: domain.Money{Amount: value, Currency: effectiveCcy},
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
			fmt.Fprintln(w, "ID\tTYPE\tNOM\tTICKER\tGROUPE\tDEVISE\tALIAS\tRETENUE")
			for _, as := range f.Book.Assets {
				retenue := ""
				if as.Withholding > 0 {
					retenue = fmt.Sprintf("%.0f%%", as.Withholding*100)
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
					as.ID, as.Kind, as.Name, as.Ticker, as.Group, as.Currency,
					strings.Join(as.Aliases, ","), retenue)
			}
			return w.Flush()
		},
	}
}

func assetEdit(a *app) *cobra.Command {
	var name, ticker, isin, group, ccy, withholding string
	var addAlias, rmAlias []string
	cmd := &cobra.Command{
		Use:   "edit <actif>",
		Short: "Modifie les champs passés en flag (alias, ISIN, retenue à la source…)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.mutate(func(b *domain.Book) error {
				asset, err := b.Asset(args[0])
				if err != nil {
					return err
				}
				if name != "" {
					asset.Name = name
				}
				if ticker != "" {
					asset.Ticker = ticker
				}
				if isin != "" {
					asset.ISIN = isin
				}
				if group != "" {
					asset.Group = group
				}
				if ccy != "" {
					if asset.Currency, err = domain.ParseCurrency(ccy); err != nil {
						return err
					}
				}
				for _, al := range addAlias {
					if !slices.ContainsFunc(asset.Aliases, func(x string) bool { return strings.EqualFold(x, al) }) {
						asset.Aliases = append(asset.Aliases, al)
					}
				}
				for _, al := range rmAlias {
					asset.Aliases = slices.DeleteFunc(asset.Aliases, func(x string) bool { return strings.EqualFold(x, al) })
				}
				if withholding != "" {
					if asset.Withholding, err = domain.ParsePercent(withholding); err != nil {
						return err
					}
				}
				if err := b.CheckAssetRefs(asset); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Actif %s mis à jour\n", asset.ID)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "nouveau nom")
	cmd.Flags().StringVar(&ticker, "ticker", "", "nouveau ticker Yahoo")
	cmd.Flags().StringVar(&isin, "isin", "", "nouvel ISIN")
	cmd.Flags().StringVar(&group, "group", "", "nouvelle poche")
	cmd.Flags().StringVar(&ccy, "ccy", "", "nouvelle devise de cotation")
	cmd.Flags().StringArrayVar(&addAlias, "add-alias", nil, "alias à ajouter (répétable)")
	cmd.Flags().StringArrayVar(&rmAlias, "rm-alias", nil, "alias à retirer (répétable)")
	cmd.Flags().StringVar(&withholding, "withholding", "", "retenue à la source sur dividendes, ex. 15%")
	return cmd
}

func assetRm(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "rm <actif>",
		Short: "Supprime un actif sans transaction (et purge son cache de cours)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.mutate(func(b *domain.Book) error {
				if err := b.RemoveAsset(args[0]); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Actif supprimé\n")
				return nil
			})
		},
	}
}

// enrichFromMarket completes ticker/name/currency from Yahoo; explicit flags
// always win, and any network failure downgrades to a warning.
func enrichFromMarket(cmd *cobra.Command, a *app, asset *domain.Asset, query string, nameSet, ccySet bool) {
	src := a.marketSource()
	info, err := src.Resolve(cmd.Context(), query)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "avertissement: résolution %q: %v\n", query, err)
		return
	}
	asset.Ticker = info.Symbol
	if !nameSet && info.Name != "" {
		asset.Name = info.Name
	}
	if data, err := src.Daily(cmd.Context(), asset.Ticker, domain.Today().AddDays(-7)); err == nil {
		if !ccySet && data.Currency != "" {
			asset.Currency = data.Currency
		}
	}
}
