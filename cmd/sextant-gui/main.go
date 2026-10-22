// Command sextant-gui is the Sextant desktop client, built with Fyne.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/coddan/sextant/internal/app"
	"github.com/coddan/sextant/internal/config"
	"github.com/coddan/sextant/internal/gui"
	"github.com/coddan/sextant/internal/synccmd"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-v", "--version", "version":
			fmt.Printf("sextant-gui %s\n", version)
			return
		case "-h", "--help", "help":
			usage()
			return
		}
	}

	store, err := config.Open()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sextant-gui: cannot open configuration: %v\n", err)
		os.Exit(1)
	}

	// `sextant-gui sync ...` works without a display, so it is handled first.
	if handled, err := synccmd.Run(context.Background(), store, os.Args[1:], os.Stdout, os.Stderr); handled {
		if err != nil {
			fmt.Fprintf(os.Stderr, "sextant-gui: %v\n", err)
			os.Exit(1)
		}
		return
	}

	rt := app.New(store)
	defer rt.Close()

	if err := gui.Run(rt); err != nil {
		fmt.Fprintf(os.Stderr, "sextant-gui: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`sextant-gui - Sextant desktop client

Usage:
  sextant-gui          open the desktop window
  sextant-gui version  print the version
  sextant-gui help     show this help
  sextant-gui sync ... push or pull the encrypted configuration bundle

On Linux, if the window does not appear under Wayland, run with
WAYLAND_DISPLAY="" to use X11.
`)
}
