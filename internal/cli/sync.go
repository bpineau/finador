package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"finador/internal/keyring"
)

func syncCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Force a sync with the GitHub remote (pull, and push if needed)",
		Long: "Reconcile the local working copy with the GitHub remote: push any unpushed " +
			"local changes (resolving conflicts via merge), then refresh from the remote. " +
			"Only meaningful in GitHub mode.",
		Example: "  finador sync",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, isRemote, err := a.dataSource()
			if err != nil {
				return err
			}
			if !isRemote {
				return fmt.Errorf("no remote configured — run `finador remote set <owner>/<repo>`")
			}

			// A password is needed only to merge on conflict; resolve it the same
			// way reads/writes do, keyed on the working-copy path.
			path := s.WorkingCopy()
			pw, _, err := keyring.PasswordFor(path, a.cache(), keyring.Prompt)
			if err != nil {
				return err
			}

			warnings, err := s.Sync(context.Background(), a.remoteMerge(pw))
			if err != nil {
				return remoteError(err)
			}
			printWarnings(warnings)
			fmt.Fprintf(cmd.OutOrStdout(), "Synced with %s\n", s.Describe())
			return nil
		},
	}
}
