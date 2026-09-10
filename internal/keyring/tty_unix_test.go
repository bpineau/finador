//go:build unix

package keyring

import (
	"os"
	"strings"
	"testing"
)

// TestTTYIDNamesTheDevice covers both shapes of a cache slot's terminal half:
// a pipe or a redirect has no terminal identity and shares one "notty" slot,
// while a character device is named by its device number (a real terminal, or
// here /dev/null - the same syscall path, with no keychain involved).
func TestTTYIDNamesTheDevice(t *testing.T) {
	pipe, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pipe.Close()
	defer w.Close()
	withStdin(t, pipe)
	if got := ttyID(); got != "notty" {
		t.Errorf("ttyID over a pipe = %q, want notty", got)
	}

	dev, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer dev.Close()
	withStdin(t, dev)
	got := ttyID()
	if !strings.HasPrefix(got, "tty") || got == "notty" {
		t.Errorf("ttyID over a character device = %q, want a tty<device> name", got)
	}
}

// withStdin swaps the process stdin for the duration of the test.
func withStdin(t *testing.T, f *os.File) {
	t.Helper()
	saved := os.Stdin
	os.Stdin = f
	t.Cleanup(func() { os.Stdin = saved })
}
