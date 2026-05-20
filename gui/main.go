package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	mode := flag.String("mode", "gui", "Run mode: gui or web")
	port := flag.Int("port", 8080, "Port for web mode")
	enableMock := flag.Bool("mock", false, "Enable mock server (web mode)")
	outputDir := flag.String("output", "postit-data", "Data output directory")
	flag.Parse()

	deps, err := initApp(*outputDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing: %v\n", err)
		os.Exit(1)
	}

	switch *mode {
	case "gui":
		runGUI(deps)
	case "web":
		if err := runWeb(deps, *port, *enableMock); err != nil {
			fmt.Fprintf(os.Stderr, "Web server error: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "Unknown mode: %s (use 'gui' or 'web')\n", *mode)
		os.Exit(1)
	}
}
