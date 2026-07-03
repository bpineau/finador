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
	"finador/internal/web"
)

const defaultTTL = 12 * time.Hour

// Option configures the CLI - tests inject a fake market source.
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

// New assembles the root cobra command with every subcommand wired to a
// shared app (global flags, market source, remote backend). main executes it;
// tests execute it too, with WithSource/WithRemoteBackend fakes.
func New(opts ...Option) *cobra.Command {
	a := &app{}
	for _, opt := range opts {
		opt(a)
	}
	root := &cobra.Command{
		Use:   "finador",
		Short: "Encrypted personal wealth tracker - CLI and web, single binary",
		Long: "finador tracks your wealth in one encrypted file.\n" +
			"Pick a noun - account, asset, cash, tx - and its subcommands guide you from there.",
		SilenceUsage:  true,
		SilenceErrors: true, // main prints them, once
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
// (then stored). A missing token is tolerated - the backend surfaces
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

// staticPW wraps an already-resolved password as a provider.
func staticPW(pw string) func() (string, error) {
	return func() (string, error) { return pw, nil }
}

// walletPassword returns a lazy resolver for the wallet password, keyed on path.
// It consults env/keychain/prompt at most once and caches a freshly typed one,
// so a command that may not need it (a conflict-free `sync`) prompts only when a
// merge actually has to decrypt - and never prompts twice.
func (a *app) walletPassword(path string) func() (string, error) {
	var (
		pw   string
		done bool
	)
	return func() (string, error) {
		if done {
			return pw, nil
		}
		cache := a.cache()
		p, fresh, err := keyring.PasswordFor(path, cache, keyring.Prompt)
		if err != nil {
			return "", err
		}
		if fresh {
			cache.Put(keyring.Key(path), p, defaultTTL)
		}
		pw, done = p, true
		return pw, nil
	}
}

// remoteMerge adapts the interactive conflict resolver to a remote.MergeFunc:
// it merges the remote copy (remotePath) into the local working copy (localPath),
// writing the reconciled result back to localPath. The password is resolved
// lazily (getPW) so callers that may never merge don't prompt for nothing.
func (a *app) remoteMerge(getPW func() (string, error)) remote.MergeFunc {
	return func(localPath, remotePath string) error {
		pw, err := getPW()
		if err != nil {
			return err
		}
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
// successful open - never cache a password that didn't decrypt anything.
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
// rewrites the file and rotates .bak - read-only commands use open() instead.
// Remote mode pulls a fresh base, mutates the working copy, then pushes it.
func (a *app) mutate(fn func(*domain.Book) error) error {
	return a.mutateFile(func(f *store.File) error {
		if err := fn(f.Book); err != nil {
			return err
		}
		return f.Save()
	})
}

// mutateFile is the single write path. It also serves operations that work on
// the whole File rather than just the Book (e.g. compact, which rewrites the
// ledger). fn is responsible for persisting its change (f.Save() / f.Compact()).
// Local mode: open, apply. Remote mode: pull a fresh base (ForWrite), apply,
// push (AfterWrite) - so EVERY write fetches-before and pushes-after.
func (a *app) mutateFile(fn func(*store.File) error) error {
	s, isRemote, err := a.dataSource()
	if err != nil {
		return err
	}
	if !isRemote {
		f, err := a.open()
		if err != nil {
			return err
		}
		return fn(f)
	}

	ctx := context.Background()
	path := s.WorkingCopy()
	cache := a.cache()
	pw, fresh, err := keyring.PasswordFor(path, cache, keyring.Prompt)
	if err != nil {
		return err
	}

	warnings, err := s.ForWrite(ctx, a.remoteMerge(staticPW(pw)))
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
	if err := fn(f); err != nil {
		return err
	}

	warnings, err = s.AfterWrite(ctx, a.commitMessage(), a.remoteMerge(staticPW(pw)))
	if err != nil {
		return remoteError(err)
	}
	printWarnings(warnings)
	return nil
}

// webSync builds the remote wiring the web server uses after every ledger
// write, so browser edits reach the remote and are protected from a later
// clobbering pull - the same fetch-after-write guarantee mutateFile gives the
// CLI. Returns nil in local mode (the server then just saves). The password is
// resolved once here, from the cache primed by open(), never per request.
//
// Push reports whether AfterWrite's conflict reconciliation rewrote the working
// copy (detected by a stamp change): if so the server reloads its File via
// Reload, picking up records merged in from a concurrent writer (e.g. Android).
// Commit messages carry no entity names - they land in the remote's history.
func (a *app) webSync() (*web.Sync, error) {
	s, isRemote, err := a.dataSource()
	if err != nil {
		return nil, err
	}
	if !isRemote {
		return nil, nil
	}
	pw, _, err := keyring.PasswordFor(s.WorkingCopy(), a.cache(), keyring.Prompt)
	if err != nil {
		return nil, err
	}
	merge := a.remoteMerge(staticPW(pw))
	copyPath := s.WorkingCopy()
	return &web.Sync{
		Push: func(ctx context.Context, msg string) (bool, error) {
			before := statStamp(copyPath)
			warnings, perr := s.AfterWrite(ctx, "finador "+msg, merge)
			printWarnings(warnings)
			if perr != nil {
				return false, perr
			}
			return statStamp(copyPath) != before, nil // changed => a merge rewrote it
		},
		Reload: func() (*store.File, error) {
			return store.Open(copyPath, pw)
		},
	}, nil
}

// statStamp returns a (size, mtime) fingerprint of a file, or the zero value if
// it cannot be stat'd. Used to tell whether a push reconciliation rewrote the
// working copy under us.
func statStamp(path string) [2]int64 {
	info, err := os.Stat(path)
	if err != nil {
		return [2]int64{}
	}
	return [2]int64{info.Size(), info.ModTime().UnixNano()}
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
		return fmt.Errorf("GitHub rejected the token - it may be expired or lack Contents access to the repo; regenerate it and run `finador remote login`")
	case errors.Is(err, remote.ErrOffline):
		return fmt.Errorf("offline and no local copy available - connect and retry: %w", err)
	case errors.Is(err, remote.ErrRemoteConflict):
		return fmt.Errorf("the remote changed and couldn't be reconciled automatically - run `finador sync` and retry")
	case errors.Is(err, remote.ErrRemoteMissing):
		return fmt.Errorf("no file found at the configured remote path/branch - check `finador remote show` (a wrong --path/--branch, a missing repo, or a token without access are the usual causes); for a genuinely new repo, run `finador init` or `finador remote adopt`")
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
