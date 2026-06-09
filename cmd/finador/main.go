package main

import (
	"fmt"
	"os"

	"finador/internal/cli"
)

func main() {
	if err := cli.New().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "finador:", err)
		os.Exit(1)
	}
}
