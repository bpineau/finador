package main

import (
	"fmt"
	"os"

	"finador/internal/cli"
	"finador/internal/paths"
)

func main() {
	paths.Migrate(os.Stderr) // once, before any command opens a file
	if err := cli.New().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "finador:", err)
		os.Exit(1)
	}
}
