package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"finador/internal/keyring"
	"finador/internal/remote"
)

func remoteCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remote",
		Short: "Configure an optional private GitHub repo as the data store",
		Long: "Keep the encrypted ledger in a private GitHub repo, synced automatically. " +
			"Local file mode stays the default and the fallback; GitHub is opt-in.\n\n" +
			"The repo only ever holds the encrypted .fin file; the market cache stays local.",
		Example: "  finador remote set bpineau/finador-data\n" +
			"  finador remote login\n" +
			"  finador remote show",
	}
	cmd.AddCommand(remoteSet(a), remoteShow(a), remoteOff(a), remoteLogin(a))
	return cmd
}

func remoteSet(a *app) *cobra.Command {
	var path, branch string
	cmd := &cobra.Command{
		Use:   "set <owner>/<repo>",
		Short: "Switch to GitHub mode, pointing at a repo",
		Long: "Write the config so finador stores the ledger in the given GitHub repo. " +
			"Run `finador remote login` once to store the token, then any command syncs " +
			"transparently. Use `--db` or FINADOR_DB to force local mode for a single run.",
		Example: "  finador remote set bpineau/finador-data --path portfolio.fin --branch main",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			owner, repo, ok := strings.Cut(args[0], "/")
			if !ok || owner == "" || repo == "" {
				return fmt.Errorf("expected <owner>/<repo>, got %q", args[0])
			}
			cfg := remote.Config{
				Source: "github",
				GitHub: &remote.GitHub{
					Owner:  owner,
					Repo:   repo,
					Path:   path,
					Branch: branch,
				},
			}
			if err := remote.Save(cfg); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"Remote set to github:%s/%s/%s@%s — run `finador remote login` to store the token\n",
				owner, repo, path, branch)
			return nil
		},
	}
	cmd.Flags().StringVar(&path, "path", "portfolio.fin", "path of the .fin file inside the repo")
	cmd.Flags().StringVar(&branch, "branch", "main", "branch to read and write")
	return cmd
}

func remoteShow(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "show",
		Short:   "Show the current mode, repo and sync state (never the token)",
		Example: "  finador remote show",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			cfg, err := remote.Load()
			if err != nil {
				return err
			}
			if cfg.Source != "github" || cfg.GitHub == nil {
				fmt.Fprintln(out, "mode:   local")
				return nil
			}
			gh := cfg.GitHub
			fmt.Fprintln(out, "mode:   github")
			fmt.Fprintf(out, "repo:   %s/%s\n", gh.Owner, gh.Repo)
			fmt.Fprintf(out, "path:   %s\n", gh.Path)
			fmt.Fprintf(out, "branch: %s\n", gh.Branch)

			// Build a syncer to read the local sync state. No network here.
			backend := a.remoteBackend
			if backend == nil {
				backend = remote.NewGitHub(*gh, "")
			}
			s, err := remote.NewSyncer(backend, *gh, cfg.ReadPullDuration())
			if err != nil {
				return err
			}
			sha, lastPull, dirty := s.Status()
			shortSHA := sha
			if len(shortSHA) > 7 {
				shortSHA = shortSHA[:7]
			}
			if shortSHA == "" {
				shortSHA = "(none)"
			}
			lp := "(never)"
			if !lastPull.IsZero() {
				lp = lastPull.Local().Format("2006-01-02 15:04:05")
			}
			fmt.Fprintf(out, "sha:    %s\n", shortSHA)
			fmt.Fprintf(out, "pulled: %s\n", lp)
			fmt.Fprintf(out, "dirty:  %t\n", dirty)
			return nil
		},
	}
}

func remoteOff(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "off",
		Short:   "Switch back to local file mode",
		Example: "  finador remote off",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := remote.Save(remote.Config{Source: "local"}); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Switched to local mode")
			return nil
		},
	}
}

func remoteLogin(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "login",
		Short: "Store the GitHub token in the keychain (re-enter to rotate)",
		Long: "Prompt for a fine-grained GitHub PAT (Contents: read and write, scoped to the " +
			"data repo) and store it in the keychain. `finador lock` forgets it again.",
		Example: "  finador remote login",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := remote.Load()
			if err != nil {
				return err
			}
			if cfg.Source != "github" || cfg.GitHub == nil {
				return fmt.Errorf("no remote configured — run `finador remote set <owner>/<repo>` first")
			}
			token, err := keyring.Prompt("GitHub token: ")
			if err != nil {
				return err
			}
			if token == "" {
				return fmt.Errorf("empty token rejected")
			}
			keyring.PutSecret(a.cache(), tokenKey(cfg), token)
			fmt.Fprintln(cmd.OutOrStdout(), "GitHub token stored")
			return nil
		},
	}
}
