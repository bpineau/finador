// Package keyring obtains the database password: environment, then a per-terminal
// cache (macOS Keychain), then an interactive no-echo prompt.
package keyring

import (
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/term"
)

// Cache remembers the password of a (file, terminal) pair for a while.
type Cache interface {
	Get(key string) (password string, ok bool)
	Put(key, password string, ttl time.Duration)
	Purge()
}

// Disabled returns a cache that remembers nothing (--no-keychain, tests).
func Disabled() Cache { return nop{} }

type nop struct{}

func (nop) Get(string) (string, bool)         { return "", false }
func (nop) Put(string, string, time.Duration) {}
func (nop) Purge()                            {}

// Key identifies the cache slot: one per database file and terminal device,
// so each terminal gets its own grace period.
func Key(dbPath string) string { return dbPath + "@" + ttyID() }

// Prompt reads a password without echo from the controlling terminal.
func Prompt(label string) (string, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", errors.New("no terminal to type the password: use FINADOR_PASSWORD")
	}
	fmt.Fprint(os.Stderr, label)
	defer fmt.Fprintln(os.Stderr)
	pw, err := term.ReadPassword(int(os.Stdin.Fd()))
	return string(pw), err
}

// PasswordFor finds the password for db: $FINADOR_PASSWORD, then cache, then
// prompt. fresh reports that the user just typed it (worth caching after a
// successful open — not before, we don't want to cache a wrong password).
func PasswordFor(db string, cache Cache, prompt func(string) (string, error)) (pw string, fresh bool, err error) {
	if pw := os.Getenv("FINADOR_PASSWORD"); pw != "" {
		return pw, false, nil
	}
	if pw, ok := cache.Get(Key(db)); ok {
		return pw, false, nil
	}
	pw, err = prompt("Password: ")
	return pw, true, err
}
