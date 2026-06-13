package cli

import (
	"cmp"
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/shopspring/decimal"
	"github.com/spf13/cobra"

	"finador/internal/domain"
)

func assetCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "asset",
		Short: "Declare and record activity on securities and properties",
		Long: `Manage assets (securities and properties) and record all activity on them.

Which subcommand to use:
  buy/sell      — quoted security you trade: requires a quantity and a price.
  dividend/fee  — income or cost on a security (no quantity, just an amount).
  set           — observed value of a property or unlisted holding; the gap
                  since the previous statement counts as performance.
  add/edit/rm   — declare, update or remove an asset definition.`,
		Example: "  finador asset buy CW8 20 @450 2024-01-20",
	}
	cmd.AddCommand(
		assetAdd(a),
		tradeCmd(a, "buy", domain.Buy, "Record a buy of a quoted security (quantity + price)", true),
		tradeCmd(a, "sell", domain.Sell, "Record a sell of a quoted security (quantity + price)", false),
		assetDividend(a),
		assetFee(a),
		assetSet(a),
		assetList(a),
		assetEdit(a),
		assetRm(a),
	)
	return cmd
}

func assetAdd(a *app) *cobra.Command {
	var kind, name, isin, ccy, group string
	var aliases []string
	cmd := &cobra.Command{
		Use:   "add <ticker|name>",
		Short: "Declare an asset: Yahoo ticker for a security, name for a property",
		Example: "  finador asset add CW8.PA --group equities/world\n" +
			"  finador asset add \"Appart Lyon\" --kind property --group realestate",
		Args: cobra.ExactArgs(1),
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
			asset.ID = domain.AssetID(domain.NewID())
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
	return cmd
}

func assetDividend(a *app) *cobra.Command {
	return assetIncomeCmd(a, "dividend", domain.Dividend,
		"Record a dividend received on a security",
		"  finador asset dividend CW8 42.50 --account \"PEA BforBank\"")
}

func assetFee(a *app) *cobra.Command {
	return assetIncomeCmd(a, "fee", domain.Fee,
		"Record a broker fee or cost on a security",
		"  finador asset fee CW8 9.90 --note courtage")
}

func assetIncomeCmd(a *app, use string, kind domain.TxKind, short, example string) *cobra.Command {
	var account, note, ccy string
	var labels []string
	cmd := &cobra.Command{
		Use:     use + " <asset> <amount> [date]",
		Short:   short,
		Example: example,
		Args:    cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.mutate(func(b *domain.Book) error {
				asset, err := resolveOrCreateSecurity(cmd, a, b, args[0], "", ccy)
				if err != nil {
					return err
				}
				amount, err := decimal.NewFromString(args[1])
				if err != nil {
					return fmt.Errorf("invalid amount %q: %w", args[1], err)
				}
				date := domain.Today()
				if len(args) == 3 {
					if date, err = domain.ParseDate(args[2]); err != nil {
						return err
					}
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
					Date:    date,
					Account: acc.ID,
					Asset:   asset.ID,
					Kind:    kind,
					Amount:  domain.Money{Amount: amount.Abs(), Currency: effectiveCcy},
					Note:    note,
				})
				fmt.Fprintf(cmd.OutOrStdout(), "[%s] %s %s: %s on %s (%s)\n",
					tx.ID, tx.Kind, asset.Name, tx.Amount, tx.Date, acc.Name)
				if err := applyLabels(b, acc.ID, asset.ID, labels); err != nil {
					return err
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&account, "account", "", "account (name or id)")
	cmd.Flags().StringVar(&note, "note", "", "free note")
	cmd.Flags().StringVar(&ccy, "ccy", "", "currency (default: asset currency)")
	cmd.Flags().StringArrayVar(&labels, "label", nil, "tag the (account, asset) pair with this label (repeatable)")
	return cmd
}

func assetSet(a *app) *cobra.Command {
	var at, account, ccy string
	var labels []string
	cmd := &cobra.Command{
		Use:     "set <asset> <value>",
		Short:   "Set a dated valuation (properties, unlisted holdings)",
		Example: "  finador asset set \"Appart Lyon\" 270000 --account \"Patrimoine immo\" --at 2024-01-01",
		Args:    cobra.ExactArgs(2),
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
				fmt.Fprintf(cmd.OutOrStdout(), "[%s] %s = %s on %s\n", tx.ID, asset.Name, tx.Amount, tx.Date)
				if err := applyLabels(b, acc.ID, asset.ID, labels); err != nil {
					return err
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&at, "at", "", "date YYYY-MM-DD (default: today)")
	cmd.Flags().StringVar(&account, "account", "", "account (name or id)")
	cmd.Flags().StringVar(&ccy, "ccy", "", "currency (default: asset currency)")
	cmd.Flags().StringArrayVar(&labels, "label", nil, "tag the (account, asset) pair with this label (repeatable)")
	return cmd
}

func assetList(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Short:   "List assets",
		Example: "  finador asset list",
		Args:    cobra.NoArgs,
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
		Use:     "edit <asset>",
		Short:   "Edit fields passed as flags (aliases, ISIN, withholding tax…)",
		Example: "  finador asset edit cw8 --group equities/world --withholding 15%",
		Args:    cobra.ExactArgs(1),
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
				asset.Aliases = applyAliasEdits(asset.Aliases, addAlias, rmAlias)
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
		Use:     "rm <asset>",
		Short:   "Delete an asset with no transactions (and purge its quote cache)",
		Example: "  finador asset rm \"Appart Lyon\"",
		Args:    cobra.ExactArgs(1),
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

// resolveOrCreateSecurity resolves ref in the book, or — when it is unknown —
// creates a security (ticker = ref), enriching from Yahoo when online, like
// `asset add`. Ambiguous refs propagate (never auto-create over an ambiguity).
func resolveOrCreateSecurity(cmd *cobra.Command, a *app, b *domain.Book, ref, group, ccy string) (*domain.Asset, error) {
	if as, err := b.Asset(ref); err == nil {
		return as, nil
	} else if !errors.Is(err, domain.ErrNotFound) {
		return nil, err // ambiguity — never mask it
	}
	ccyParsed, err := currencyOr(ccy, domain.EUR)
	if err != nil {
		return nil, err
	}
	asset := &domain.Asset{
		ID:       domain.AssetID(domain.NewID()),
		Kind:     domain.Security,
		Name:     ref,
		Ticker:   ref,
		Currency: ccyParsed,
		Group:    group,
	}
	if !a.offline {
		enrichFromMarket(cmd, a, asset, ref, false, ccy != "")
	}
	if err := b.AddAsset(asset); err != nil {
		return nil, err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Asset %s (%s) created\n", asset.Name, asset.ID)
	return asset, nil
}

// applyLabels tags the (account, asset) pair with each label name; it silently
// ignores ErrDuplicate (the tag is already there) but surfaces any other error.
func applyLabels(b *domain.Book, acc domain.AccountID, asset domain.AssetID, names []string) error {
	for _, name := range names {
		err := b.AddLabel(&domain.Label{
			ID:      domain.LabelID(domain.NewID()),
			Account: acc,
			Asset:   asset,
			Name:    name,
		})
		if err != nil && !errors.Is(err, domain.ErrDuplicate) {
			return err
		}
	}
	return nil
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
