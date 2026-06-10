package cli

import (
	"fmt"
	"maps"
	"slices"

	"github.com/spf13/cobra"

	"finador/internal/domain"
)

func configCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Settings: default-account, keychain-ttl, risk-free…"}
	set := &cobra.Command{
		Use:  "set <key> <value>",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.mutate(func(b *domain.Book) error {
				b.Config[args[0]] = args[1]
				return nil
			})
		},
	}
	get := &cobra.Command{
		Use:  "get [key]",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f, err := a.open()
			if err != nil {
				return err
			}
			if len(args) == 1 {
				fmt.Fprintln(cmd.OutOrStdout(), f.Book.Config[args[0]])
				return nil
			}
			for _, k := range slices.Sorted(maps.Keys(f.Book.Config)) {
				fmt.Fprintf(cmd.OutOrStdout(), "%s = %s\n", k, f.Book.Config[k])
			}
			return nil
		},
	}
	cmd.AddCommand(set, get)
	return cmd
}
