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
	var format, account, since string
	var createMissing, noGuard, reconcile, dryRun bool
	cmd := &cobra.Command{
		Use:   "import <file>",
		Short: "Import transactions from a CSV file or a broker statement (re-import is idempotent)",
		Long: `Import transactions, skipping the ones already imported.

Formats:
  csv   - one row per transaction, columns matched by header (the default).
  ibkr  - an Interactive Brokers activity statement (Reports > Statements >
          Activity, CSV): trades, dividends, withholding tax, deposits and
          withdrawals of the one account it covers.

A statement often covers trades already entered by hand, which carry no
fingerprint for the dedup rule to compare. Such a line is matched against the
ledger and left out, naming the manual entry it belongs to; --reconcile adopts
that entry instead (it takes the statement's fingerprint and nothing else
changes), so replaying the statement is idempotent from then on. --since skips
the years already booked, --no-guard imports everything regardless, and
--dry-run reports without writing.`,
		Example: "  finador import transactions.csv\n" +
			"  finador import --format ibkr --account \"CTO Meridia\" --dry-run ActivityStatement.csv\n" +
			"  finador import --format ibkr --account \"CTO Meridia\" --since 2026-01-01 --reconcile ActivityStatement.csv",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			file, err := os.Open(args[0])
			if err != nil {
				return err
			}
			defer file.Close()
			switch format {
			case "csv":
				if account != "" || createMissing || since != "" || noGuard || reconcile {
					return errors.New("--account, --create-missing, --since, --no-guard and --reconcile apply to --format ibkr only")
				}
				return runImportCSV(cmd, a, file, dryRun)
			case "ibkr":
				opts := ibkr.Options{Account: account, CreateMissing: createMissing}
				if noGuard && reconcile {
					return errors.New("--no-guard and --reconcile are contradictory: the first imports the line, the second adopts the manual entry")
				}
				switch {
				case noGuard:
					opts.Guard = ibkr.GuardOff
				case reconcile:
					opts.Guard = ibkr.GuardReconcile
				}
				if since != "" {
					if opts.Since, err = domain.ParseDate(since); err != nil {
						return err
					}
				}
				return runImportIBKR(cmd, a, file, opts, dryRun)
			}
			return fmt.Errorf("unknown import format %q (expected csv or ibkr)", format)
		},
	}
	cmd.Flags().StringVar(&format, "format", "csv", "source format: csv or ibkr")
	cmd.Flags().StringVar(&account, "account", "", "account the statement belongs to (--format ibkr)")
	cmd.Flags().BoolVar(&createMissing, "create-missing", false,
		"declare the securities the statement names and the book does not (--format ibkr)")
	cmd.Flags().StringVar(&since, "since", "",
		"ignore the statement lines dated before YYYY-MM-DD (--format ibkr)")
	cmd.Flags().BoolVar(&noGuard, "no-guard", false,
		"import the lines a hand-entered transaction already records (--format ibkr)")
	cmd.Flags().BoolVar(&reconcile, "reconcile", false,
		"adopt the hand-entered transactions a line matches, instead of skipping the line (--format ibkr)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report what would be done, write nothing")
	return cmd
}

func runImportCSV(cmd *cobra.Command, a *app, file io.Reader, dryRun bool) error {
	var added, skipped int
	// mutate only writes the file if the whole import succeeded.
	if err := a.applyImport(dryRun, func(b *domain.Book) error {
		var err error
		added, skipped, err = portfolio.ImportCSV(b, file)
		return err
	}); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%d imported, %d skipped (duplicates)%s\n", added, skipped, dryRunSuffix(dryRun))
	return nil
}

func runImportIBKR(cmd *cobra.Command, a *app, file io.Reader, opts ibkr.Options, dryRun bool) error {
	var res ibkr.Result
	if err := a.applyImport(dryRun, func(b *domain.Book) error {
		var err error
		res, err = ibkr.Import(b, file, opts)
		return err
	}); err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "%s%s\n", res.Summary(), dryRunSuffix(dryRun))
	for _, match := range res.Matches {
		fmt.Fprintln(out, match)
	}
	if ignored := res.IgnoredReport(); ignored != "" {
		fmt.Fprintf(out, "not imported: %s\n", ignored)
	}
	return nil
}

// applyImport runs an import through the normal write path, or - in dry-run -
// against a book that is opened, mutated in memory and never saved. Same code
// reads the statement either way: only the save differs, so the report of a
// dry run is exactly what the real import would do.
func (a *app) applyImport(dryRun bool, fn func(*domain.Book) error) error {
	if !dryRun {
		return a.mutate(fn)
	}
	f, err := a.open()
	if err != nil {
		return err
	}
	return fn(f.Book)
}

// dryRunSuffix marks a report that changed nothing on disk.
func dryRunSuffix(dryRun bool) string {
	if dryRun {
		return " (dry run: nothing written)"
	}
	return ""
}
