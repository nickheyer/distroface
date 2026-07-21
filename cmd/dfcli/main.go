// DistroFace command line client
package main

import (
	"fmt"
	"os"

	"github.com/nickheyer/distroface/pkg/api"
)

// Set at build time via ldflags
var Version = "dev"

func main() {
	if err := api.NewRootCmd(Version).Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
