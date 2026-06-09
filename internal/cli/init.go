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
		Use:   "init",
		Short: "Crée le fichier de données chiffré",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pw := os.Getenv("FINADOR_PASSWORD")
			if pw == "" {
				var err error
				if pw, err = askTwice(); err != nil {
					return err
				}
			}
			if _, err := store.Create(a.dbPath, pw); err != nil {
				return err
			}
			a.cache().Put(keyring.Key(a.dbPath), pw, defaultTTL)
			fmt.Fprintf(cmd.OutOrStdout(), "Créé %s\n", a.dbPath)
			return nil
		},
	}
}

func askTwice() (string, error) {
	p1, err := keyring.Prompt("Mot de passe : ")
	if err != nil {
		return "", err
	}
	p2, err := keyring.Prompt("Confirmez : ")
	if err != nil {
		return "", err
	}
	if p1 != p2 {
		return "", errors.New("les mots de passe diffèrent")
	}
	if p1 == "" {
		return "", errors.New("mot de passe vide refusé")
	}
	return p1, nil
}
