package keyring

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// fakeRun simulates /usr/bin/security over an account → payload map.
func fakeRun(entries map[string]string) func(args ...string) (string, error) {
	return func(args ...string) (string, error) {
		switch args[0] {
		case "find-generic-password":
			if p, ok := entries[args[4]]; ok { // args: find -s finador -a <key> -w
				return p, nil
			}
			return "", errors.New("not found")
		case "add-generic-password":
			// args: add-generic-password -U -s finador -a <key> -w <payload>
			entries[args[5]] = args[7]
			return "", nil
		case "delete-generic-password":
			for k := range entries {
				delete(entries, k)
				return "", nil
			}
			return "", errors.New("empty")
		}
		return "", fmt.Errorf("commande inattendue %v", args)
	}
}

func testKeychain(entries map[string]string, now time.Time) *keychain {
	return &keychain{now: func() time.Time { return now }, run: fakeRun(entries)}
}

// fakeRunHexOnNonPrintable reproduces the real behavior of security(1):
// `find-generic-password -w` returns a HEX DUMP as soon as the stored value
// contains a non-printable byte (e.g. a \n), and the raw value otherwise.
func fakeRunHexOnNonPrintable(entries map[string]string) func(args ...string) (string, error) {
	faithful := fakeRun(entries)
	return func(args ...string) (string, error) {
		out, err := faithful(args...)
		if err == nil && args[0] == "find-generic-password" {
			for i := 0; i < len(out); i++ {
				if out[i] < 0x20 { // non-printable → security encodes as hex
					return hex.EncodeToString([]byte(out)), nil
				}
			}
		}
		return out, err
	}
}

// TestKeychainSurvivesSecurityHexDump: with security's real behavior
// (hex as soon as there's a \n), the round-trip must work - that's what
// base64 storage guarantees (the old "expiry\npassword" format failed here).
func TestKeychainSurvivesSecurityHexDump(t *testing.T) {
	entries := map[string]string{}
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	k := &keychain{now: func() time.Time { return now }, run: fakeRunHexOnNonPrintable(entries)}
	k.Put("db@tty1", "s3cret avec espace", time.Hour)

	for _, v := range entries { // the stored value must not contain any \n
		if strings.ContainsRune(v, '\n') {
			t.Fatalf("valeur stockée avec un \\n (security la rendrait en hex): %q", v)
		}
	}
	if pw, ok := k.Get("db@tty1"); !ok || pw != "s3cret avec espace" {
		t.Fatalf("round-trip à travers le hex de security = %q, %v", pw, ok)
	}
}

func TestKeychainPutGet(t *testing.T) {
	entries := map[string]string{}
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	k := testKeychain(entries, now)
	k.Put("db@tty1", "s3cret", time.Hour)

	if pw, ok := k.Get("db@tty1"); !ok || pw != "s3cret" {
		t.Fatalf("Get = %q, %v", pw, ok)
	}
	if _, ok := k.Get("autre@tty1"); ok {
		t.Fatal("Get d'une clé inconnue devrait échouer")
	}
}

func TestKeychainTTLExpiry(t *testing.T) {
	entries := map[string]string{}
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	k := testKeychain(entries, now)
	k.Put("db@tty1", "s3cret", time.Hour)

	k.now = func() time.Time { return now.Add(2 * time.Hour) }
	if _, ok := k.Get("db@tty1"); ok {
		t.Fatal("le mot de passe aurait dû expirer")
	}
}

func TestKeychainPurge(t *testing.T) {
	entries := map[string]string{"a": "1", "b": "2"}
	k := testKeychain(entries, time.Now())
	k.Purge()
	if len(entries) != 0 {
		t.Fatalf("Purge incomplet: %v", entries)
	}
}

func TestPasswordForEnv(t *testing.T) {
	t.Setenv("FINADOR_PASSWORD", "env-pw")
	pw, fresh, err := PasswordFor("/tmp/x.fin", Disabled(), nil)
	if err != nil || pw != "env-pw" || fresh {
		t.Fatalf("pw=%q fresh=%v err=%v", pw, fresh, err)
	}
}

func TestPasswordForPrompt(t *testing.T) {
	t.Setenv("FINADOR_PASSWORD", "")
	prompt := func(string) (string, error) { return "typed", nil }
	pw, fresh, err := PasswordFor("/tmp/x.fin", Disabled(), prompt)
	if err != nil || pw != "typed" || !fresh {
		t.Fatalf("pw=%q fresh=%v err=%v", pw, fresh, err)
	}
}

func TestKeyIsPerFileAndTerminal(t *testing.T) {
	if k := Key("/tmp/a.fin"); !strings.HasPrefix(k, "/tmp/a.fin@") {
		t.Fatalf("Key = %q", k)
	}
}
