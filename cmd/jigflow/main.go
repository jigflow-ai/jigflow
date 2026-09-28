// Command jigflow (alias jfl) runs a Playbook inside existing coding agents.
package main

import (
	"fmt"
	"os"

	"github.com/jigflow-ai/jigflow/internal/cli"
)

func main() {
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "jfl:", err)
		os.Exit(1)
	}
	os.Exit(cli.Run(os.Args[1:], dir, os.Stdout, os.Stderr))
}
