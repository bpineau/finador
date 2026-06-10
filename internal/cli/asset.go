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
	cmd := &cobra.Command{Use: "asset", Short: "Manage assets: listed securities and properties"}
	cmd.AddCommand(assetAdd(a), assetSet(a), assetList(a), assetEdit(a), assetRm(a))
	return cmd
}

func assetAdd(a *app) *cobra.Command {
	var kind, name, isin, ccy, group, id string
	var aliases []string
	cmd := &cobra.Command{
		Use:   "add <ticker|name>",
		Short: "Declare an asset: Yahoo ticker for a security, name for a property",
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
				fmt.Fprintf(cmd.OutOrStdout(), "Asset %s (%s) created\n", asset.Name, asset.ID)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&kind, "kind", "security", "security or property")
	cmd.Flags().StringVar(&name, "name", "", "name (default: the argument)")
	cmd.Flags().StringVar(&isin, "isin", "", "ISIN code")
	cmd.Flags().StringArrayVar(&aliases, "alias", nil, "additional alias (repeatable)")
	cmd.Flags().StringVar(&ccy, "ccy", "EUR", "quote currency")
	cmd.Flags().StringVar(&group, "group", "", "hierarchical group, e.g. equities/us/tech")
	cmd.Flags().StringVar(&id, "id", "", "identifier (default: slug of the name)")
	return cmd
}

func assetSet(a *app) *cobra.Command {
	var at, account, ccy string
	cmd := &cobra.Command{
		Use:   "set <asset> <value>",
		Short: "Set a dated valuation (properties, unlisted holdings)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.mutate(func(b *domain.Book) error {
				asset, err := b.Asset(args[0])
				if err != nil {
					return err
				}
				value, err := decimal.NewFromString(args[1])
				if err != nil {
					return fmt.Errorf("invalid value %q: %w", args[1], err)
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
				fmt.Fprintf(cmd.OutOrStdout(), "[%d] %s = %s on %s\n", tx.ID, asset.Name, tx.Amount, tx.Date)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&at, "at", "", "date YYYY-MM-DD (default: today)")
	cmd.Flags().StringVar(&account, "account", "", "account (name or id)")
	cmd.Flags().StringVar(&ccy, "ccy", "", "currency (default: asset currency)")
	return cmd
}

func assetList(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List assets",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, err := a.open()
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tTYPE\tNAME\tTICKER\tGROUP\tCURRENCY\tALIASES\tWITHHOLDING")
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
		Use:   "edit <asset>",
		Short: "Edit fields passed as flags (aliases, ISIN, withholding tax…)",
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
				fmt.Fprintf(cmd.OutOrStdout(), "Asset %s updated\n", asset.ID)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new name")
	cmd.Flags().StringVar(&ticker, "ticker", "", "new Yahoo ticker")
	cmd.Flags().StringVar(&isin, "isin", "", "new ISIN")
	cmd.Flags().StringVar(&group, "group", "", "new group")
	cmd.Flags().StringVar(&ccy, "ccy", "", "new quote currency")
	cmd.Flags().StringArrayVar(&addAlias, "add-alias", nil, "alias to add (repeatable)")
	cmd.Flags().StringArrayVar(&rmAlias, "rm-alias", nil, "alias to remove (repeatable)")
	cmd.Flags().StringVar(&withholding, "withholding", "", "withholding tax on dividends, e.g. 15%")
	return cmd
}

func assetRm(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "rm <asset>",
		Short: "Delete an asset with no transactions (and purge its quote cache)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.mutate(func(b *domain.Book) error {
				if err := b.RemoveAsset(args[0]); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Asset deleted\n")
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
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: resolving %q: %v\n", query, err)
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
