package main

import (
	"embed"
	"fmt"
	"os"

	"github.com/kleist-dev/logmcp/cmd"
)

//go:embed docs/*.md
var docsFS embed.FS

func main() {
	if err := cmd.Execute(docsFS); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
