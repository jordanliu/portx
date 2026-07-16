package main

import (
	"context"
	"os"

	"portx/internal/cli"
)

func main() {
	os.Exit(cli.Run(context.Background(), os.Args))
}
