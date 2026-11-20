// server-man-firecracker-runtime is the fixed root-only systemd ExecStartPre/
// ExecStopPost hook. It has no sudo rule, network listener or shell execution.
package main

import (
	"fmt"
	"os"
	"server-man/internal/firecrackerhost"
)

func main() {
	if err := firecrackerhost.RuntimeMain(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
