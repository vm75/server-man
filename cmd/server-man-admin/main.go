// server-man-admin is installed root-owned and may be invoked without arguments
// through a restricted sudoers rule. It never starts a network listener.
package main

import (
	"context"
	"fmt"
	"os"
	"serverman/internal/admin"
	"serverman/internal/config"
	"serverman/internal/core"
	"syscall"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) != 1 || os.Geteuid() != 0 {
		return fmt.Errorf("helper requires root and no arguments")
	}
	// Do not inherit user-controlled execution context, home, or executable paths.
	os.Clearenv()
	for k, v := range map[string]string{"PATH": "/usr/sbin:/usr/bin:/sbin:/bin", "HOME": "/root", "LANG": "C", "LC_ALL": "C"} {
		if err := os.Setenv(k, v); err != nil {
			return err
		}
	}
	if err := os.Chdir("/"); err != nil {
		return err
	}
	// A root-owned directory prevents lock replacement by the invoking user.
	if err := os.MkdirAll("/run/server-man-admin", 0700); err != nil {
		return err
	}
	info, err := os.Lstat("/run/server-man-admin")
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || stat.Uid != 0 || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("unsafe helper runtime directory")
	}
	fd, err := syscall.Open("/run/server-man-admin/lock", syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	defer syscall.Close(fd)
	if err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("another privileged operation is running")
	}
	cfg, err := config.Defaults()
	if err != nil {
		return err
	}
	cfg.StateDir = "/var/lib/server-man-admin"
	// Privileged configuration paths are fixed, independently of X's app config.
	return admin.Serve(context.Background(), cfg, core.Runner{}, os.Stdin, os.Stdout)
}
