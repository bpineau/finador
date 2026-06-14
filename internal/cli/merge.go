package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"finador/internal/domain"
	"finador/internal/keyring"
	"finador/internal/store"
)

func mergeCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "merge <other-file>",
		Short: "Reconcile two diverged copies of the same ledger",
		Long: "Merge another copy of this ledger (same passphrase) into the current one. " +
			"Additions, deletions and edits of distinct entries union with no loss; when " +
			"both copies edited the same entry, the last edit wins by timestamp; a true tie " +
			"(same entry, same instant, different values) prompts you to choose.\n\n" +
			"It expects copies of the SAME ledger (same file id) — it refuses to merge unrelated files.",
		Example: "  finador merge ../laptop2/finador.fin",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// `merge` operates on the local file (a.dbPath). In GitHub mode that
			// file isn't the synced copy, and the result wouldn't be pushed — so
			// refuse rather than write to the wrong place. Remote copies already
			// reconcile automatically on sync.
			if _, isRemote, derr := a.dataSource(); derr != nil {
				return derr
			} else if isRemote {
				return fmt.Errorf("`merge` is for local-file mode — in GitHub mode diverged copies reconcile automatically on `finador sync`")
			}
			cache := a.cache()
			pw, fresh, err := keyring.PasswordFor(a.dbPath, cache, keyring.Prompt)
			if err != nil {
				return err
			}
			dst, err := store.Open(a.dbPath, pw)
			if err != nil {
				return err
			}
			if fresh {
				cache.Put(keyring.Key(a.dbPath), pw, configTTL(dst.Book))
			}
			other, err := store.Open(args[0], pw)
			if err != nil {
				if errors.Is(err, domain.ErrBadPassword) {
					return fmt.Errorf("cannot open %s: wrong password or corrupted file (the two copies must share the same passphrase)", args[0])
				}
				return err
			}

			resolver := mergeResolver(cmd.OutOrStdout(), cmd.InOrStdin())
			stats, err := dst.Merge(other, resolver)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "merged %d entities (%d from the other file), %d conflicts resolved\n",
				stats.Entities, stats.FromOther, stats.Conflicts)
			return nil
		},
	}
}

// mergeResolver returns a conflict resolver that prompts interactively on the
// given streams. If stdin is not a terminal, it returns an error listing the
// conflict instead of guessing — a non-interactive merge with conflicts must
// fail clearly, not hang or pick silently.
func mergeResolver(out io.Writer, in io.Reader) func(store.Conflict) (int, error) {
	interactive := false
	if f, ok := in.(*os.File); ok {
		interactive = term.IsTerminal(int(f.Fd()))
	}
	r := bufio.NewReader(in)

	return func(c store.Conflict) (int, error) {
		if !interactive {
			var b strings.Builder
			fmt.Fprintf(&b, "merge conflict on %s %s at %s — both copies edited it at the same instant with different values:\n",
				c.Class, c.ID, c.Ts)
			for i, cand := range c.Candidates {
				fmt.Fprintf(&b, "  [%d] %s\n", i+1, cand)
			}
			b.WriteString("re-run interactively to choose")
			return 0, errors.New(b.String())
		}

		fmt.Fprintf(out, "\nConflict on %s %s at %s — both copies edited it at the same instant:\n", c.Class, c.ID, c.Ts)
		for i, cand := range c.Candidates {
			fmt.Fprintf(out, "  [%d] %s\n", i+1, cand)
		}
		for {
			fmt.Fprintf(out, "keep which? [1-%d]: ", len(c.Candidates))
			line, err := r.ReadString('\n')
			if err != nil && line == "" {
				return 0, fmt.Errorf("merge aborted: %w", err)
			}
			n, perr := strconv.Atoi(strings.TrimSpace(line))
			if perr == nil && n >= 1 && n <= len(c.Candidates) {
				return n - 1, nil
			}
			fmt.Fprintf(out, "please enter a number between 1 and %d\n", len(c.Candidates))
		}
	}
}
