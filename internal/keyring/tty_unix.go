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
	// Device numbers can be recycled across terminals: at worst another
	// terminal of the same user inherits the cache - the same trust boundary
	// as the Keychain itself.
	return fmt.Sprintf("tty%d", st.Rdev)
}
