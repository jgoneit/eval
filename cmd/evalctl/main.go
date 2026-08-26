package main

import (
	"context"
	"os"

	"github.com/jgoneit/eval/internal/cli"
)

func main() {
	os.Exit(cli.Run(context.Background(), os.Args[1:], cli.Runtime{}))
}
