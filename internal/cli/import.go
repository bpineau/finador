package cli

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"finador/internal/domain"
	"finador/internal/importer/ibkr"
	"finador/internal/portfolio"
)

func importCmd(a *app) *cobra.Command {
	var format, account string
	var createMissing bool
	cmd := &cobra.Command{
		Use:   "import <file>",
		Short: "Import transactions from a CSV file or a broker statement (re-import is idempotent)",
		Long: `Import transactions, skipping the ones already imported.

Formats:
  csv   - one row per transaction, columns matched by header (the default).
  ibkr  - an Interactive Brokers activity statement (Reports > Statements >
          Activity, CSV): trades, dividends, withholding tax, deposits and
          withdrawals of the one account it covers.`,
		Example: "  finador import transactions.csv\n" +
			"  finador import --format ibkr --account \"CTO Meridia\" ActivityStatement.csv",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			file, err := os.Open(args[0])
			if err != nil {
				return err
			}
			defer file.Close()
			switch format {
			case "csv":
				if account != "" || createMissing {
					return errors.New("--account and --create-missing apply to --format ibkr only")
				}
				return runImportCSV(cmd, a, file)
			case "ibkr":
				return runImportIBKR(cmd, a, file, ibkr.Options{Account: account, CreateMissing: createMissing})
			}
			return fmt.Errorf("unknown import format %q (expected csv or ibkr)", format)
		},
	}
	cmd.Flags().StringVar(&format, "format", "csv", "source format: csv or ibkr")
	cmd.Flags().StringVar(&account, "account", "", "account the statement belongs to (--format ibkr)")
	cmd.Flags().BoolVar(&createMissing, "create-missing", false,
		"declare the securities the statement names and the book does not (--format ibkr)")
	return cmd
}

func runImportCSV(cmd *cobra.Command, a *app, file io.Reader) error {
	var added, skipped int
	// mutate only writes the file if the whole import succeeded.
	if err := a.mutate(func(b *domain.Book) error {
		var err error
		added, skipped, err = portfolio.ImportCSV(b, file)
		return err
	}); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%d imported, %d skipped (duplicates)\n", added, skipped)
	return nil
}

func runImportIBKR(cmd *cobra.Command, a *app, file io.Reader, opts ibkr.Options) error {
	var res ibkr.Result
	if err := a.mutate(func(b *domain.Book) error {
		var err error
		res, err = ibkr.Import(b, file, opts)
		return err
	}); err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "%d imported, %d skipped (duplicates)\n", res.Added, res.Skipped)
	if ignored := res.IgnoredReport(); ignored != "" {
		fmt.Fprintf(out, "not imported: %s\n", ignored)
	}
	return nil
}
