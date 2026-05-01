package main

import (
	"context"
	"os"

	"github.com/solodov/recall-org-roam/internal/app"
	"github.com/solodov/recall-org-roam/internal/cli"
)

// main wires the Cobra command surface to the application service.
func main() {
	os.Exit(cli.Run(context.Background(), os.Args[1:], os.Stdout, os.Stderr, app.NewService()))
}
