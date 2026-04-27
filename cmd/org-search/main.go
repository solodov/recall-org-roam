package main

import (
	"context"
	"os"

	"org-search/internal/app"
	"org-search/internal/cli"
)

// main wires the Cobra command surface to the application service.
func main() {
	os.Exit(cli.Run(context.Background(), os.Args[1:], os.Stdout, os.Stderr, app.NewService()))
}
