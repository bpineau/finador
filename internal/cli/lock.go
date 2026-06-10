package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"finador/internal/keyring"
)

func lockCmd(_ *app) *cobra.Command {
	return &cobra.Command{
		Use:   "lock",
		Short: "Oublie les mots de passe mémorisés dans le Keychain",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			keyring.System().Purge()
			fmt.Fprintln(cmd.OutOrStdout(), "Keychain purgé")
			return nil
		},
	}
}
