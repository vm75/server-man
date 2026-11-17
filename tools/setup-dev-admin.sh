#!/bin/sh
# tools/setup-dev-admin.sh - Setup or remove server-man-admin privileged helper for development
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"

account=$(id -un)
if [ "$(id -u)" = 0 ]; then
    echo "Error: Run this script as your normal development account, not root." >&2
    echo "The script will use sudo to install the helper binary and sudoers rule." >&2
    exit 1
fi

uninstall=false
case "${1:-}" in
    --uninstall|--remove|-u)
        uninstall=true
        ;;
    --help|-h)
        cat <<EOF
Usage: $0 [--uninstall]

Set up the privileged helper binary and sudoers rule for server-man development.
This enables 'privileged_operations: true' in development mode without requiring
a full system installation or systemd service.

Options:
  --uninstall, -u    Remove /usr/local/libexec/server-man-admin and /etc/sudoers.d/server-man
  --help, -h         Show this help message
EOF
        exit 0
        ;;
    "")
        ;;
    *)
        echo "Unknown option: $1 (use --help for usage)" >&2
        exit 1
        ;;
esac

if [ "$uninstall" = true ]; then
    echo "Removing privileged helper for development..."
    if [ -f /etc/sudoers.d/server-man ]; then
        echo "Removing /etc/sudoers.d/server-man..."
        sudo rm -f /etc/sudoers.d/server-man
    fi
    if [ -f /usr/local/libexec/server-man-admin ]; then
        echo "Removing /usr/local/libexec/server-man-admin..."
        sudo rm -f /usr/local/libexec/server-man-admin
    fi
    echo "Privileged helper uninstalled successfully."
    exit 0
fi

echo "=== Setting up server-man privileged helper for development ==="

# 1. Build the helper executable
echo "--> Building bin/server-man-admin..."
mkdir -p "$root/bin"
CGO_ENABLED=0 go build -trimpath -o "$root/bin/server-man-admin" ./cmd/server-man-admin

# 2. Install the binary to /usr/local/libexec
echo "--> Installing /usr/local/libexec/server-man-admin (requires sudo)..."
sudo install -d -m 0755 /usr/local/libexec
sudo install -m 0755 "$root/bin/server-man-admin" /usr/local/libexec/server-man-admin

# 3. Create and validate the sudoers rule
echo "--> Installing sudoers rule in /etc/sudoers.d/server-man..."
rule="$account ALL=(root) NOPASSWD: NOSETENV: /usr/local/libexec/server-man-admin \"\""
tmp_sudoers=$(mktemp)
printf '%s\n' "$rule" > "$tmp_sudoers"

sudo install -d -m 0755 /etc/sudoers.d
sudo install -m 0440 "$tmp_sudoers" /etc/sudoers.d/server-man
rm -f "$tmp_sudoers"

# Validate sudoers syntax
if ! sudo visudo -cf /etc/sudoers.d/server-man; then
    echo "Error: visudo check failed on /etc/sudoers.d/server-man! Removing rule for safety." >&2
    sudo rm -f /etc/sudoers.d/server-man
    exit 1
fi

# 4. Verify passwordless execution
echo "--> Verifying passwordless execution of the privileged helper..."
if ! printf '{"operation":"services_list","arguments":{"scope":"system"}}\n' | sudo -n /usr/local/libexec/server-man-admin >/dev/null 2>&1; then
    if ! sudo -n true 2>/dev/null; then
        echo "Warning: sudo -n check failed. Please ensure your user has sudo privileges." >&2
    fi
fi

# 5. Check configuration file
config_file="${XDG_CONFIG_HOME:-$HOME/.config}/server-man/server-man.yaml"
echo "--> Checking configuration file: $config_file"
if [ -f "$config_file" ]; then
    if grep -Eq '^[[:space:]]*privileged_operations:[[:space:]]*true' "$config_file"; then
        echo "  [OK] privileged_operations: true is enabled in $config_file"
    else
        echo "  [Notice] privileged_operations is not set to true in $config_file."
        echo "           Ensure 'privileged_operations: true' is added under 'server:' in that file."
    fi
else
    echo "  [Notice] Config file $config_file does not exist yet."
    echo "           Create it with 'privileged_operations: true' under 'server:'."
fi

echo ""
echo "Setup complete! The dev server (make api) can now execute privileged operations."
