package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"finador/internal/keyring"
	"finador/internal/store"
)

func initCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "init",
		Short:   "Create the encrypted data file",
		Example: "  finador init",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pw := os.Getenv("FINADOR_PASSWORD")
			if pw == "" {
				var err error
				if pw, err = askTwice(); err != nil {
					return err
				}
			}
			f, err := store.Create(a.dbPath, pw)
			if err != nil {
				return err
			}
			a.cache().Put(keyring.Key(a.dbPath), pw, configTTL(f.Book)) // best-effort
			fmt.Fprintf(cmd.OutOrStdout(), "Created %s\n", a.dbPath)
			return nil
		},
	}
}

func askTwice() (string, error) {
	p1, err := keyring.Prompt("Password: ")
	if err != nil {
		return "", err
	}
	p2, err := keyring.Prompt("Confirm: ")
	if err != nil {
		return "", err
	}
	if p1 != p2 {
		return "", errors.New("passwords do not match")
	}
	if p1 == "" {
		return "", errors.New("empty password rejected")
	}
	return p1, nil
}
