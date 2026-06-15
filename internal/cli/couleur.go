package cli

import (
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

const (
	ansiVert    = "\x1b[32m"
	ansiGarance = "\x1b[31m"
	ansiReset   = "\x1b[0m"
)

// colorsEnabled: never with --no-color or NO_COLOR; always with
// FINADOR_FORCE_COLOR (tests); otherwise only on a real terminal.
func (a *app) colorsEnabled(cmd *cobra.Command) bool {
	if a.noColor || os.Getenv("NO_COLOR") != "" {
		return false
	}
	if os.Getenv("FINADOR_FORCE_COLOR") != "" {
		return true
	}
	f, ok := cmd.OutOrStdout().(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// tint colors a value cell by its sign when enabled.
func tint(s string, sign float64, enabled bool) string {
	if !enabled || sign == 0 {
		return s
	}
	if sign > 0 {
		return ansiVert + s + ansiReset
	}
	return ansiGarance + s + ansiReset
}
