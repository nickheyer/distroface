package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/nickheyer/distroface/internal/config"
	"github.com/nickheyer/distroface/internal/server"
)

func main() {
	// PARSE FLAGS
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = *flag.String("config", "config.yml", "path to config file")
		flag.Parse()
	}

	// LOAD CONFIG
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	// CREATE AND START SERVER
	srv, err := server.NewServer(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create server: %v\n", err)
		os.Exit(1)
	}

	log.Printf("Starting registry server on port %s", cfg.Server.Port)
	if err := srv.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
}
