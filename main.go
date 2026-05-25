package main

import (
	"embed"
	"fmt"
	"os"

	"github.com/kleist-dev/logmcp/cmd"
)

//go:embed docs/index.md docs/CONFIG.md docs/LOGGING.md docs/ANSIBLE.md
var docsFS embed.FS

func main() {
	if err := cmd.Execute(docsFS); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
