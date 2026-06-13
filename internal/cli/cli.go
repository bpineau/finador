// Package cli is the thin command-line facade over the finador engine.
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"finador/internal/domain"
	"finador/internal/keyring"
	"finador/internal/market"
	"finador/internal/store"
)

const defaultTTL = 12 * time.Hour

// Option configures the CLI — tests inject a fake market source.
type Option func(*app)

// WithSource replaces the default Yahoo market source.
func WithSource(s market.Source) Option { return func(a *app) { a.source = s } }

// app carries the persistent flags shared by every command.
type app struct {
	dbPath     string
	noKeychain bool
	offline    bool
	noColor    bool
	source     market.Source
}

func New(opts ...Option) *cobra.Command {
	a := &app{}
	for _, opt := range opts {
		opt(a)
	}
	root := &cobra.Command{
		Use:           "finador",
		Short:         "Encrypted personal wealth tracker — CLI and web, single binary",
		SilenceUsage:  true,
		SilenceErrors: true, // main les affiche, une seule fois
	}
	root.PersistentFlags().StringVar(&a.dbPath, "db", defaultDB(), "encrypted data file")
	root.PersistentFlags().BoolVar(&a.noKeychain, "no-keychain", false, "do not store the password in the keychain")
	root.PersistentFlags().BoolVar(&a.offline, "offline", false, "never access the network (cache only)")
	root.PersistentFlags().BoolVar(&a.noColor, "no-color", false, "disable colors")
	root.AddCommand(initCmd(a), accountCmd(a), assetCmd(a), addCmd(a), sellCmd(a),
		cashCmd(a), depositCmd(a), withdrawCmd(a), txCmd(a), importCmd(a),
		configCmd(a), lockCmd(a), valueCmd(a), refreshCmd(a), perfCmd(a), chartCmd(a),
		serveCmd(a))
	return root
}

func (a *app) marketSource() market.Source {
	if a.source == nil {
		a.source = market.NewYahoo()
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

// open decrypts the database; a freshly typed password is cached only after a
// successful open — never cache a password that didn't decrypt anything.
func (a *app) open() (*store.File, error) {
	cache := a.cache()
	pw, fresh, err := keyring.PasswordFor(a.dbPath, cache, keyring.Prompt)
	if err != nil {
		return nil, err
	}
	f, err := store.Open(a.dbPath, pw)
	if err != nil {
		return nil, err
	}
	if fresh {
		cache.Put(keyring.Key(a.dbPath), pw, configTTL(f.Book))
	}
	return f, nil
}

// mutate opens, applies fn to the book, then saves atomically.
// If fn fails, nothing is written. fn is assumed to mutate: a no-op fn still
// rewrites the file and rotates .bak — read-only commands use open() instead.
func (a *app) mutate(fn func(*domain.Book) error) error {
	f, err := a.open()
	if err != nil {
		return err
	}
	if err := fn(f.Book); err != nil {
		return err
	}
	return f.Save()
}

// configTTL reads the Keychain TTL from the book config ("keychain-ttl": "8h"),
// defaulting to 12h.
func configTTL(b *domain.Book) time.Duration {
	if d, err := time.ParseDuration(b.Config["keychain-ttl"]); err == nil && d > 0 {
		return d
	}
	return defaultTTL
}
