//go:build unix

package keyring

import (
	"fmt"
	"os"
	"syscall"
)

// ttyID names the terminal device of stdin, or "notty" for pipes and scripts.
func ttyID() string {
	info, err := os.Stdin.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return "notty"
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "notty"
	}
	// Les numéros de device peuvent être recyclés entre terminaux : au pire un
	// autre terminal du même utilisateur hérite du cache — même frontière de
	// confiance que le Keychain lui-même.
	return fmt.Sprintf("tty%d", st.Rdev)
}
