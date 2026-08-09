package cli

import (
	"bytes"
	"fmt"
	"maps"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"finador/internal/domain"
	"finador/internal/remote"
)

// configKey documents one setting the ledger understands: what it means, and
// what finador does when it is unset. This table is the single source `config
// get` reads from - a key finador honours but does not list here is a key
// nobody can discover.
type configKey struct {
	Name    string
	Default string
	Doc     string
}

var configKeys = []configKey{
	{"currency", "EUR", "display currency"},
	{"default-account", "", "envelope used when a command omits --account"},
	{"keychain-ttl", "12h", "how long the wallet password stays in the keychain"},
	{"risk-free", "0%", "annualized risk-free rate for Sharpe and Sortino"},
}

// configDefault returns the documented default of a key, and whether finador
// knows the key at all.
func configDefault(name string) (string, bool) {
	for _, k := range configKeys {
		if k.Name == name {
			return k.Default, true
		}
	}
	return "", false
}

func configCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Settings: default-account, keychain-ttl, risk-free…"}
	set := &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a config key",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, known := configDefault(args[0]); !known {
				// Warn, never refuse: a newer finador may read keys this one ignores.
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: unknown config key %q (see `finador config get`)\n", args[0])
			}
			return a.mutate(func(b *domain.Book) error {
				b.Config[args[0]] = args[1]
				return nil
			})
		},
	}
	get := &cobra.Command{
		Use:   "get [key]",
		Short: "Show the whole configuration: paths, values in effect, defaults",
		Example: "  finador config get                 # everything, defaults included\n" +
			"  finador config get keychain-ttl    # one raw value, for scripts",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f, err := a.open()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(args) == 1 {
				value, ok := f.Book.Config[args[0]]
				if !ok {
					value, _ = configDefault(args[0])
				}
				fmt.Fprintln(out, value)
				return nil
			}

			// Where the settings live, before what they say.
			fmt.Fprintf(out, "# ledger: %s\n", a.dbPath)
			if path, perr := remote.ConfigPath(); perr == nil {
				source := "local"
				if cfg, cerr := remote.Load(); cerr == nil && cfg.Source != "" {
					source = cfg.Source
				}
				fmt.Fprintf(out, "# config: %s   (source = %s)\n", path, source)
			}

			// Aligned in a buffer, then trimmed: a key with no comment must not
			// end on the padding of a column it does not fill.
			var table bytes.Buffer
			w := tabwriter.NewWriter(&table, 2, 4, 1, ' ', 0)
			for _, k := range configKeys {
				value, ok := f.Book.Config[k.Name]
				comment := ""
				if !ok {
					value, comment = k.Default, "# default"
					if value == "" {
						comment = "# unset"
					}
				}
				fmt.Fprintf(w, "%s\t= %s\t%s\n", k.Name, value, comment)
			}
			for _, name := range slices.Sorted(maps.Keys(f.Book.Config)) {
				if _, known := configDefault(name); !known {
					fmt.Fprintf(w, "%s\t= %s\t# unknown\n", name, f.Book.Config[name])
				}
			}
			if err := w.Flush(); err != nil {
				return err
			}
			for line := range strings.SplitSeq(strings.TrimRight(table.String(), "\n"), "\n") {
				fmt.Fprintln(out, strings.TrimRight(line, " "))
			}
			return nil
		},
	}
	cmd.AddCommand(set, get)
	return cmd
}
