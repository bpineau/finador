package cli

import (
	"context"
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

			s, isRemote, err := a.dataSource()
			if err != nil {
				return err
			}
			path := a.dbPath
			if isRemote {
				if err := s.EnsureDir(); err != nil {
					return err
				}
				path = s.WorkingCopy()
			}

			f, err := store.Create(path, pw)
			if err != nil {
				return err
			}
			a.cache().Put(keyring.Key(path), pw, configTTL(f.Book)) // best-effort

			if isRemote {
				// First push creates (or, if the remote already has a file, merges
				// into) the remote — the sync layer handles ErrRemoteMissing→create.
				warnings, perr := s.AfterWrite(context.Background(), "finador init", a.remoteMerge(staticPW(pw)))
				if perr != nil {
					return remoteError(perr)
				}
				printWarnings(warnings)
				fmt.Fprintf(cmd.OutOrStdout(), "Created %s (remote: %s)\n", path, s.Describe())
				return nil
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Created %s\n", path)
			return nil
		},
	}
}

func askTwice() (string, error) {
	p1, err := keyring.Prompt("Wallet password: ")
	if err != nil {
		return "", err
	}
	p2, err := keyring.Prompt("Confirm wallet password: ")
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
