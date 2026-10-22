// Command sextant is the Sextant terminal client.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/coddan/sextant/internal/app"
	"github.com/coddan/sextant/internal/config"
	"github.com/coddan/sextant/internal/synccmd"
	"github.com/coddan/sextant/internal/tui"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-v", "--version", "version":
			fmt.Printf("sextant %s\n", version)
			return
		case "-h", "--help", "help":
			usage()
			return
		}
	}

	store, err := config.Open()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sextant: cannot open configuration: %v\n", err)
		os.Exit(1)
	}

	// `sextant sync ...` works without a display, so it is handled before the TUI.
	if handled, err := synccmd.Run(context.Background(), store, os.Args[1:], os.Stdout, os.Stderr); handled {
		if err != nil {
			fmt.Fprintf(os.Stderr, "sextant: %v\n", err)
			os.Exit(1)
		}
		return
	}

	rt := app.New(store)
	defer rt.Close()

	if err := tui.Run(rt); err != nil {
		fmt.Fprintf(os.Stderr, "sextant: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`sextant - Sextant terminal client

Usage:
  sextant            open the dashboard
  sextant version    print the version
  sextant help       show this help
  sextant sync ...   push or pull the encrypted configuration bundle

The configuration lives in ~/.config/sextant. Set SEXTANT_PORTABLE=1 (or place
a "sextant.portable" file next to the executable) to keep it beside the binary
instead.
`)
}
