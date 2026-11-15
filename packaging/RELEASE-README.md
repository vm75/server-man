# server-man release installation

This archive contains the web/API application, privileged helper and shared installer. Go, Node.js and Make are not needed to install or run it. Linux with systemd on Ubuntu, Debian or Arch is required. Native packages must be available in your configured repositories.

Verify the archive against its separately distributed SHA256 checksum before extracting it. Inspect the plan, then install for an existing non-root workload account:

```sh
./install.sh --user YOUR_ACCOUNT --backends all --dry-run
./install.sh --user YOUR_ACCOUNT --backends all
```

The second command uses sudo and asks for confirmation. Select individual backends with `--backends podman,docker,virtualization,incus,samba,nfs`; `--skip-packages` uses installed packages. `--yes` accepts the displayed plan without prompting. The installer enables a system service running as your workload account and grants selected backend groups plus the validated privileged helper rule. These capabilities grant broad host control. Native package installation may start daemons; Arch setup includes a full upgrade.

Open http://127.0.0.1:8080 and sign in with the private `~/.config/server-man/token`. Manage the app with `sudo systemctl status server-man` and `sudo journalctl -u server-man`. The single configuration is `~/.config/server-man/server-man.yaml`, containing `server` settings and all managed resource sections. Existing YAML is preserved, including its write policy and listen address. Server-setting changes require a server-man restart; resource changes use reviewed apply. No workloads or shares are created; Incus storage/network initialization remains explicit. See `docs/installation.md` for details. Backup/restore remains unfinished and deferred.
