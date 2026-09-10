package keyring

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/term"
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

// TestSystemPlatformSwitch: only macOS has a password cache. Elsewhere
// System must hand back the no-op one rather than shelling out to a
// /usr/bin/security that does not exist.
func TestSystemPlatformSwitch(t *testing.T) {
	c := System()
	_, isNop := c.(nop)
	if want := runtime.GOOS != "darwin"; isNop != want {
		t.Fatalf("System() is the no-op cache = %v on %s, want %v", isNop, runtime.GOOS, want)
	}
	if runtime.GOOS != "darwin" {
		return
	}
	k, ok := c.(*keychain)
	if !ok || k.now == nil || k.run == nil {
		t.Fatalf("System() = %T, want a wired *keychain", c)
	}
}

// TestRunSecurityReportsFailure exercises the real runner on its error path
// only: an unknown subcommand never reaches the keychain, and the contract
// (empty output plus the exit error) is what Get relies on to report
// not-found. The success path is covered by the fake runner everywhere else.
func TestRunSecurityReportsFailure(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("no /usr/bin/security outside macOS")
	}
	out, err := runSecurity("finador-no-such-subcommand")
	if err == nil {
		t.Fatalf("a bogus subcommand should fail, got out=%q", out)
	}
	if out != "" {
		t.Errorf("output on failure = %q, want empty", out)
	}
}

// TestKeychainGetRejectsUnreadableEntries: everything Get cannot trust reads
// as not-found, so the user simply retypes the password and the entry is
// rewritten in the current format - never a hard error, never a stale hit.
func TestKeychainGetRejectsUnreadableEntries(t *testing.T) {
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	b64 := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	future := now.Add(time.Hour).Unix()

	cases := []struct {
		name   string
		stored string
		want   string // "" means not-found
	}{
		{"good", b64(fmt.Sprintf("%d\ns3cret", future)), "s3cret"},
		{"not base64 (pre-base64 format)", "12345\ns3cret", ""},
		{"no separator", b64("s3cret"), ""},
		{"unparsable expiry", b64("soon\ns3cret"), ""},
		{"expired", b64(fmt.Sprintf("%d\ns3cret", now.Add(-time.Second).Unix())), ""},
		{"password with newlines", b64(fmt.Sprintf("%d\nline1\nline2", future)), "line1\nline2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k := testKeychain(map[string]string{"db@tty1": tc.stored}, now)
			pw, ok := k.Get("db@tty1")
			if ok != (tc.want != "") || pw != tc.want {
				t.Fatalf("Get = %q, %v; want %q, %v", pw, ok, tc.want, tc.want != "")
			}
		})
	}
}

// The disabled cache must stay a silent sink: Put and Purge are called on
// every --no-keychain run and must not panic nor remember anything.
func TestDisabledCacheForgetsEverything(t *testing.T) {
	c := Disabled()
	c.Put("db@tty1", "s3cret", time.Hour)
	c.Purge()
	if pw, ok := c.Get("db@tty1"); ok || pw != "" {
		t.Fatalf("Get = %q, %v; the disabled cache must remember nothing", pw, ok)
	}
}

// TestPasswordForPrecedence pins the whole ladder: the environment wins, then
// the cache, then the prompt - and only a typed password is fresh (worth
// caching once the file actually opened with it).
func TestPasswordForPrecedence(t *testing.T) {
	const db = "/tmp/x.fin"
	cached := newFakeCache()
	cached.entries[Key(db)] = "cached-pw"
	boom := errors.New("no terminal")

	cases := []struct {
		name      string
		env       string
		cache     Cache
		prompt    func(string) (string, error)
		wantPw    string
		wantFresh bool
		wantErr   error
	}{
		{"environment first", "env-pw", cached, nil, "env-pw", false, nil},
		{"then the cache", "", cached, nil, "cached-pw", false, nil},
		{"then the prompt", "", Disabled(), func(string) (string, error) { return "typed", nil }, "typed", true, nil},
		{"prompt failure propagates", "", Disabled(), func(string) (string, error) { return "", boom }, "", true, boom},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FINADOR_PASSWORD", tc.env)
			pw, fresh, err := PasswordFor(db, tc.cache, tc.prompt)
			if pw != tc.wantPw || fresh != tc.wantFresh || !errors.Is(err, tc.wantErr) {
				t.Fatalf("PasswordFor = %q, %v, %v; want %q, %v, %v",
					pw, fresh, err, tc.wantPw, tc.wantFresh, tc.wantErr)
			}
		})
	}
}

// Prompt needs a controlling terminal; a piped or scripted run must be told
// to use the environment variable instead of hanging or reading garbage.
func TestPromptWithoutTerminal(t *testing.T) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		t.Skip("stdin is a terminal: the no-terminal path cannot be exercised")
	}
	if _, err := Prompt("Wallet password: "); err == nil ||
		!strings.Contains(err.Error(), "FINADOR_PASSWORD") {
		t.Fatalf("err = %v, want the no-terminal error naming the fallback", err)
	}
}

// Cache slots are per file AND per terminal: two databases never share a
// grace period, and the key is stable across calls in one process.
func TestKeyIsStableAndPerFile(t *testing.T) {
	a, b := Key("/tmp/a.fin"), Key("/tmp/b.fin")
	if a == b {
		t.Fatalf("two databases share the slot %q", a)
	}
	if a != Key("/tmp/a.fin") {
		t.Error("Key is not stable within one process")
	}
	if tty := strings.TrimPrefix(a, "/tmp/a.fin@"); tty == "" {
		t.Error("Key carries no terminal identity")
	}
}
