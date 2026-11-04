package main

import (
	"fmt"
	"os"

	"github.com/nickheyer/distroface/internal/container"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// Close runs on every exit path once the app exists
func run() error {
	app, err := container.New()
	if err != nil {
		return fmt.Errorf("failed to initialize: %w", err)
	}
	defer app.Close()
	if err := app.Start(); err != nil {
		return fmt.Errorf("server error: %w", err)
	}
	return nil
}
