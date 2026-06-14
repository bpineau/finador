package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
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

			// The wallet password is needed only to merge on conflict; resolve it
			// lazily so a conflict-free sync never prompts (and a freshly typed one
			// is cached, fixing the per-sync re-prompt).
			warnings, err := s.Sync(context.Background(), a.remoteMerge(a.walletPassword(s.WorkingCopy())))
			if err != nil {
				return remoteError(err)
			}
			printWarnings(warnings)
			if !s.HasWorkingCopy() {
				fmt.Fprintf(cmd.OutOrStdout(),
					"no file found at %s — check the --path and --branch above (a mismatch is the usual cause); "+
						"for a genuinely new repo, run `finador init` or `finador remote adopt`\n",
					s.Describe())
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Synced with %s\n", s.Describe())
			return nil
		},
	}
}
