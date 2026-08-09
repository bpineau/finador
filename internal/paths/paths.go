// Package paths tells finador where its files live.
//
// One layout on every platform - the XDG one - so a path printed in the
// documentation is the path on disk:
//
//	~/.config/finador/config.json       remote configuration
//	~/.cache/finador/<id>.cache         market quote sidecar
//	~/.cache/finador/checkout/          GitHub working copy and its state
//	~/.local/share/finador/finador.fin  the default ledger
//
// Two kinds of environment variables move each directory: FINADOR_CONFIG_DIR,
// FINADOR_CACHE_DIR and FINADOR_DATA_DIR name finador's own directory, while
// XDG_CONFIG_HOME, XDG_CACHE_HOME and XDG_DATA_HOME name the base directory it
// sits in. Migrate moves data left at pre-XDG locations.
package paths

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// LedgerName is the default ledger's file name inside the data directory.
const LedgerName = "finador.fin"

// dir describes how one of finador's directories is resolved, and where it
// used to live before the XDG layout.
type dir struct {
	env      string // FINADOR_*_DIR: finador's directory itself
	xdg      string // XDG_*_HOME: the base directory
	fallback string // base directory relative to $HOME, when XDG_*_HOME is unset
	legacy   string // pre-XDG location of finador's directory, relative to $HOME
}

var (
	configDir = dir{"FINADOR_CONFIG_DIR", "XDG_CONFIG_HOME", ".config", "Library/Application Support/finador"}
	cacheDir  = dir{"FINADOR_CACHE_DIR", "XDG_CACHE_HOME", ".cache", "Library/Caches/finador"}
	dataDir   = dir{"FINADOR_DATA_DIR", "XDG_DATA_HOME", ".local/share", ""}
)

// stuck records, per FINADOR_*_DIR name, a legacy directory that keeps being
// used because Migrate could not move it; stuckLedger does the same for the
// legacy ledger file. Both are empty in the normal case, and only Migrate
// writes them, before any command runs.
var (
	stuck       = map[string]string{}
	stuckLedger string
)

// resolve returns finador's directory: the FINADOR_* override, then the XDG
// base, then $HOME and the conventional fallback.
func (d dir) resolve() (string, error) {
	if p := os.Getenv(d.env); p != "" {
		return p, nil
	}
	if p := stuck[d.env]; p != "" {
		return p, nil // a migration failed: keep reading where the data still is
	}
	if base := os.Getenv(d.xdg); filepath.IsAbs(base) {
		return filepath.Join(base, "finador"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home directory: %w", err)
	}
	return filepath.Join(home, filepath.FromSlash(d.fallback), "finador"), nil
}

// legacyPath is where d used to live, or "" when it always lived here.
func (d dir) legacyPath() string {
	home, err := os.UserHomeDir()
	if d.legacy == "" || err != nil {
		return ""
	}
	return filepath.Join(home, filepath.FromSlash(d.legacy))
}

// Config is where config.json lives.
func Config() (string, error) { return configDir.resolve() }

// Cache is where the quote sidecar and the GitHub working copy live.
func Cache() (string, error) { return cacheDir.resolve() }

// Data is where the default ledger lives.
func Data() (string, error) { return dataDir.resolve() }

// Ledger is the default ledger path, used when neither --db nor FINADOR_DB
// names one.
func Ledger() (string, error) {
	if stuckLedger != "" {
		return stuckLedger, nil
	}
	d, err := Data()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, LedgerName), nil
}

// Migrate moves data left at pre-XDG locations into the layout the resolvers
// return, and reports each move on w. It runs once, from main, before any
// command opens a file.
//
// Nothing is ever copied: a directory is renamed whole or not at all, so there
// is never a second, divergent ledger. A destination that already exists wins
// and the legacy copy is left untouched, for the user to sort out - with a
// warning on w, since data split over two directories is impossible to guess.
// An empty destination directory is not data and does not win. When a rename
// fails, the legacy location keeps being used for the rest of the process and w
// gets a warning - the data stays reachable, always.
//
// An explicit FINADOR_*_DIR or FINADOR_DB skips the migration it names: the
// user pointed at a location, finador does not tidy it.
func Migrate(w io.Writer) {
	for _, d := range []dir{configDir, cacheDir, dataDir} {
		legacy := d.legacyPath()
		if legacy == "" || os.Getenv(d.env) != "" {
			continue
		}
		to, err := d.resolve()
		if err != nil {
			continue
		}
		if !move(w, legacy, to) {
			stuck[d.env] = legacy
		}
	}
	migrateLedger(w)
}

// migrateLedger moves ~/.finador.fin, and its .bak sidecar, into the data
// directory. The keychain entry is keyed by path, so the wallet password is
// asked once more after the move - said plainly rather than left as a surprise.
func migrateLedger(w io.Writer) {
	if os.Getenv("FINADOR_DB") != "" || os.Getenv(dataDir.env) != "" {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	legacy := filepath.Join(home, "."+LedgerName)
	to, err := Ledger()
	if err != nil || !exists(legacy) {
		return
	}
	if !move(w, legacy, to) {
		stuckLedger = legacy
		return
	}
	if exists(legacy) {
		return // the destination won; move said so, and the ledger stays put
	}
	_ = os.Rename(legacy+".bak", to+".bak") // absent is the normal case
	fmt.Fprintln(w, "note: the keychain is keyed by path - the wallet password will be asked once more")
}

// move renames from to to, reporting whether the data now lives at to. A
// missing source is success with nothing to do; an existing destination wins,
// but never in silence - data in two places the user cannot guess is worse than
// data in the old place.
func move(w io.Writer, from, to string) bool {
	if from == to || !exists(from) {
		return true
	}
	if exists(to) && !clearEmptyDir(to) {
		fmt.Fprintf(w, "warning: %s still holds data - %s already exists, so merge them by hand\n", from, to)
		return true
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		fmt.Fprintf(w, "warning: keeping %s (cannot create %s: %v)\n", from, filepath.Dir(to), err)
		return false
	}
	if err := os.Rename(from, to); err != nil {
		fmt.Fprintf(w, "warning: keeping %s (cannot move it to %s: %v)\n", from, to, err)
		return false
	}
	fmt.Fprintf(w, "migrated %s -> %s\n", from, to)
	return true
}

// clearEmptyDir removes path when it is an empty directory, and reports that
// nothing stands there any more. Such a directory is what a build that resolved
// the new layout without knowing how to migrate leaves behind: it holds no
// data, so it must not outrank the data.
func clearEmptyDir(path string) bool {
	fi, err := os.Stat(path)
	if err != nil || !fi.IsDir() {
		return false // a file is data, whatever its size
	}
	return os.Remove(path) == nil // harmlessly refused when it is not empty
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
