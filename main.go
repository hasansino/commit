package main

import (
	"os"

	"github.com/hasansino/commit/internal/cmd"
)

func main() {
	os.Exit(cmd.Execute())
}
