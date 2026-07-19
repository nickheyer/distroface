package main

import (
	"fmt"
	"os"

	"github.com/nickheyer/distroface/internal/container"
)

func main() {
	app, err := container.New()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize: %v\n", err)
		os.Exit(1)
	}
	defer app.Close()

	if err := app.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
}
