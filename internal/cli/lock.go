package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"finador/internal/keyring"
)

func lockCmd(_ *app) *cobra.Command {
	return &cobra.Command{
		Use:   "lock",
		Short: "Forget the password and GitHub token stored in the keychain",
		Long: "Purge every finador secret from the keychain: the file password and, in " +
			"GitHub mode, the stored GitHub token. The next command will prompt again.",
		Example: "  finador lock",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			keyring.System().Purge()
			fmt.Fprintln(cmd.OutOrStdout(), "Keychain purged")
			return nil
		},
	}
}
