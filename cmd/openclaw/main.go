package main

import (
	"fmt"
	"os"

	"openclaw/internal/app"
)

func main() {
	if err := app.New().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
