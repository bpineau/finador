// Package cli is the thin command-line facade over the finador engine.
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"finador/internal/domain"
	"finador/internal/keyring"
	"finador/internal/market"
	"finador/internal/remote"
	"finador/internal/store"
)

const defaultTTL = 12 * time.Hour

// Option configures the CLI — tests inject a fake market source.
type Option func(*app)

// WithSource replaces the default Yahoo market source.
func WithSource(s market.Source) Option { return func(a *app) { a.source = s } }

// WithRemoteBackend injects a remote.Backend (test seam). When nil, the real
// GitHub client is built from config + token at use time.
func WithRemoteBackend(b remote.Backend) Option { return func(a *app) { a.remoteBackend = b } }

// app carries the persistent flags shared by every command.
type app struct {
	dbPath     string
	noKeychain bool
	offline    bool
	noColor    bool
	source     market.Source

	// remoteBackend, when non-nil, overrides the real GitHub backend (tests).
	remoteBackend remote.Backend
	// dbExplicit records whether --db was set on the command line; together with
	// FINADOR_DB it forces local mode regardless of the remote config.
	dbExplicit bool
	// runName is the name of the command being executed, used in commit messages.
	runName string
}

func New(opts ...Option) *cobra.Command {
	a := &app{}
	for _, opt := range opts {
		opt(a)
	}
	root := &cobra.Command{
		Use:   "finador",
		Short: "Encrypted personal wealth tracker — CLI and web, single binary",
		Long: "finador tracks your wealth in one encrypted file.\n" +
			"Pick a noun — account, asset, cash, tx — and its subcommands guide you from there.",
		SilenceUsage:  true,
		SilenceErrors: true, // main les affiche, une seule fois
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			// Capture, for the duration of this invocation, whether --db was set
			// explicitly (forces local mode) and the command name (commit message).
			a.dbExplicit = cmd.Flags().Changed("db")
			a.runName = cmd.Name()
			return nil
		},
	}
	root.PersistentFlags().StringVar(&a.dbPath, "db", defaultDB(), "encrypted data file")
	root.PersistentFlags().BoolVar(&a.noKeychain, "no-keychain", false, "do not store the password in the keychain")
	root.PersistentFlags().BoolVar(&a.offline, "offline", false, "never access the network (cache only)")
	root.PersistentFlags().BoolVar(&a.noColor, "no-color", false, "disable colors")

	root.AddGroup(
		&cobra.Group{ID: "setup", Title: "Setup:"},
		&cobra.Group{ID: "ledger", Title: "Record & correct:"},
		&cobra.Group{ID: "analyze", Title: "Analyze:"},
		&cobra.Group{ID: "ops", Title: "Sync & maintenance:"},
	)

	init_ := initCmd(a)
	init_.GroupID = "setup"

	account := accountCmd(a)
	account.GroupID = "setup"

	asset := assetCmd(a)
	asset.GroupID = "setup"

	label := labelCmd(a)
	label.GroupID = "setup"

	cfg := configCmd(a)
	cfg.GroupID = "setup"

	cash := cashCmd(a)
	cash.GroupID = "ledger"

	tx := txCmd(a)
	tx.GroupID = "ledger"

	value := valueCmd(a)
	value.GroupID = "analyze"

	perf := perfCmd(a)
	perf.GroupID = "analyze"

	chart := chartCmd(a)
	chart.GroupID = "analyze"

	export := exportCmd(a)
	export.GroupID = "analyze"

	imp := importCmd(a)
	imp.GroupID = "ops"

	refresh := refreshCmd(a)
	refresh.GroupID = "ops"

	merge := mergeCmd(a)
	merge.GroupID = "ops"

	compact := compactCmd(a)
	compact.GroupID = "ops"

	lock := lockCmd(a)
	lock.GroupID = "ops"

	serve := serveCmd(a)
	serve.GroupID = "ops"

	remoteC := remoteCmd(a)
	remoteC.GroupID = "ops"

	syncC := syncCmd(a)
	syncC.GroupID = "ops"

	root.AddCommand(
		init_, account, asset, label, cfg,
		cash, tx,
		value, perf, chart, export,
		imp, refresh, merge, compact, lock, serve,
		remoteC, syncC,
	)
	return root
}

func (a *app) marketSource() market.Source {
	if a.source == nil {
		a.source = market.Default()
	}
	return a.source
}

// ensureFresh refreshes the market cache when needed. It never fails hard:
// offline or network trouble degrade to warnings, stale data stays usable.
func (a *app) ensureFresh(cmd *cobra.Command, f *store.File) {
	if a.offline {
		return
	}
	sum := market.Refresh(cmd.Context(), f.Book, a.marketSource(), false)
	for _, w := range sum.Warnings {
		fmt.Fprintln(cmd.ErrOrStderr(), "warning:", w)
	}
	if len(sum.Fetched) > 0 {
		if err := f.SaveCache(); err != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), "warning: cache not saved:", err)
		}
	}
}

func defaultDB() string {
	if p := os.Getenv("FINADOR_DB"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".finador.fin")
}

func (a *app) cache() keyring.Cache {
	if a.noKeychain {
		return keyring.Disabled()
	}
	return keyring.System()
}

// dataSource resolves how this invocation reaches its ledger. Local mode keeps
// today's exact flow; remote mode returns a Syncer over a GitHub-backed working
// copy.
//
//   - --db set OR FINADOR_DB set        → local (nil, false, nil)
//   - config source != "github"/absent  → local (nil, false, nil)
//   - source == "github"                 → (syncer, true, err)
func (a *app) dataSource() (*remote.Syncer, bool, error) {
	if a.dbExplicit || os.Getenv("FINADOR_DB") != "" {
		return nil, false, nil
	}
	cfg, _ := remote.Load()
	if cfg.Source != "github" || cfg.GitHub == nil {
		return nil, false, nil
	}
	backend := a.remoteBackend
	if backend == nil {
		backend = remote.NewGitHub(*cfg.GitHub, a.githubToken(cfg))
	}
	s, err := remote.NewSyncer(backend, *cfg.GitHub, cfg.ReadPullDuration())
	if err != nil {
		return nil, true, err
	}
	return s, true, nil
}

// tokenKey is the keychain slot for the GitHub PAT of a given repo.
func tokenKey(cfg remote.Config) string {
	if cfg.GitHub == nil {
		return "github:"
	}
	return "github:" + cfg.GitHub.Owner + "/" + cfg.GitHub.Repo
}

// githubToken resolves the PAT: GITHUB_TOKEN env → keychain → interactive prompt
// (then stored). A missing token is tolerated — the backend surfaces
// remote.ErrRemoteAuth on the first request.
func (a *app) githubToken(cfg remote.Config) string {
	if t := os.Getenv("GITHUB_TOKEN"); t != "" {
		return t
	}
	cache := a.cache()
	key := tokenKey(cfg)
	if t, ok := keyring.GetSecret(cache, key); ok && t != "" {
		return t
	}
	t, err := keyring.Prompt("GitHub token: ")
	if err != nil || t == "" {
		return ""
	}
	keyring.PutSecret(cache, key, t)
	return t
}

// commitMessage builds a short, useful commit message for a remote write.
func (a *app) commitMessage() string {
	name := a.runName
	if name == "" {
		name = "write"
	}
	return "finador " + name + " (" + time.Now().UTC().Format("2006-01-02 15:04") + ")"
}

// remoteMerge adapts the interactive conflict resolver to a remote.MergeFunc:
// it merges the remote copy (remotePath) into the local working copy (localPath),
// writing the reconciled result back to localPath.
func (a *app) remoteMerge(pw string) remote.MergeFunc {
	return func(localPath, remotePath string) error {
		local, err := store.Open(localPath, pw)
		if err != nil {
			return fmt.Errorf("open local copy for merge: %w", err)
		}
		other, err := store.Open(remotePath, pw)
		if err != nil {
			return fmt.Errorf("open remote copy for merge: %w", err)
		}
		resolver := mergeResolver(os.Stdout, os.Stdin)
		if _, err := local.Merge(other, resolver); err != nil {
			return err
		}
		return local.Save()
	}
}

// open decrypts the database; a freshly typed password is cached only after a
// successful open — never cache a password that didn't decrypt anything.
// Remote mode reads through the sync layer first, then opens the working copy.
func (a *app) open() (*store.File, error) {
	s, isRemote, err := a.dataSource()
	if err != nil {
		return nil, err
	}
	path := a.dbPath
	if isRemote {
		warnings, ferr := s.ForRead(context.Background())
		if ferr != nil {
			return nil, remoteError(ferr)
		}
		printWarnings(warnings)
		path = s.WorkingCopy()
	}

	cache := a.cache()
	pw, fresh, err := keyring.PasswordFor(path, cache, keyring.Prompt)
	if err != nil {
		return nil, err
	}
	f, err := store.Open(path, pw)
	if err != nil {
		return nil, err
	}
	if fresh {
		cache.Put(keyring.Key(path), pw, configTTL(f.Book))
	}
	return f, nil
}

// mutate opens, applies fn to the book, then saves atomically.
// If fn fails, nothing is written. fn is assumed to mutate: a no-op fn still
// rewrites the file and rotates .bak — read-only commands use open() instead.
// Remote mode pulls a fresh base, mutates the working copy, then pushes it.
func (a *app) mutate(fn func(*domain.Book) error) error {
	s, isRemote, err := a.dataSource()
	if err != nil {
		return err
	}
	if !isRemote {
		f, err := a.open()
		if err != nil {
			return err
		}
		if err := fn(f.Book); err != nil {
			return err
		}
		return f.Save()
	}

	ctx := context.Background()
	path := s.WorkingCopy()
	cache := a.cache()
	pw, fresh, err := keyring.PasswordFor(path, cache, keyring.Prompt)
	if err != nil {
		return err
	}

	warnings, err := s.ForWrite(ctx, a.remoteMerge(pw))
	if err != nil {
		return remoteError(err)
	}
	printWarnings(warnings)

	f, err := store.Open(path, pw)
	if err != nil {
		return err
	}
	if fresh {
		cache.Put(keyring.Key(path), pw, configTTL(f.Book))
	}
	if err := fn(f.Book); err != nil {
		return err
	}
	if err := f.Save(); err != nil {
		return err
	}

	warnings, err = s.AfterWrite(ctx, a.commitMessage(), a.remoteMerge(pw))
	if err != nil {
		return remoteError(err)
	}
	printWarnings(warnings)
	return nil
}

// printWarnings emits sync warnings to stderr.
func printWarnings(warnings []string) {
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}
}

// remoteError turns the sentinel sync errors into clear, actionable messages.
func remoteError(err error) error {
	switch {
	case errors.Is(err, remote.ErrRemoteAuth):
		return fmt.Errorf("GitHub authentication failed — check the token or run `finador remote login`: %w", err)
	case errors.Is(err, remote.ErrOffline):
		return fmt.Errorf("offline and no local copy available — connect and retry: %w", err)
	default:
		return err
	}
}

// configTTL reads the Keychain TTL from the book config ("keychain-ttl": "8h"),
// defaulting to 12h.
func configTTL(b *domain.Book) time.Duration {
	if d, err := time.ParseDuration(b.Config["keychain-ttl"]); err == nil && d > 0 {
		return d
	}
	return defaultTTL
}
