// server-man-install configures a local host from a built source tree or bundle.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"server-man/internal/installation"
	"syscall"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := installation.Run(ctx, os.Args[1:], os.Stdin, os.Stdout); err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, "Installation failed:", err)
		os.Exit(1)
	}
}
