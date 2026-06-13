package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"finador/internal/keyring"
)

func lockCmd(_ *app) *cobra.Command {
	return &cobra.Command{
		Use:     "lock",
		Short:   "Forget passwords stored in the keychain",
		Example: "  finador lock",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			keyring.System().Purge()
			fmt.Fprintln(cmd.OutOrStdout(), "Keychain purged")
			return nil
		},
	}
}
