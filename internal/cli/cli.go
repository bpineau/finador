// Package cli is the thin command-line facade over the finador engine.
package cli

import (
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"finador/internal/domain"
	"finador/internal/keyring"
	"finador/internal/store"
)

const defaultTTL = 12 * time.Hour

// app carries the persistent flags shared by every command.
type app struct {
	dbPath     string
	noKeychain bool
}

func New() *cobra.Command {
	a := &app{}
	root := &cobra.Command{
		Use:           "finador",
		Short:         "Suivi de patrimoine chiffré — CLI et web, single binary",
		SilenceUsage:  true,
		SilenceErrors: true, // main les affiche, une seule fois
	}
	root.PersistentFlags().StringVar(&a.dbPath, "db", defaultDB(), "fichier de données chiffré")
	root.PersistentFlags().BoolVar(&a.noKeychain, "no-keychain", false, "ne pas mémoriser le mot de passe")
	root.AddCommand(initCmd(a), accountCmd(a), assetCmd(a), addCmd(a), sellCmd(a),
		cashCmd(a), depositCmd(a), withdrawCmd(a))
	return root
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
