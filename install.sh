#!/bin/sh
# Install Plainshow Cluster on this machine.
#
# Everything the node stores goes under one directory. The only things this
# script may put elsewhere are a symlink onto PATH and a systemd unit, and the
# node runs correctly without either — decline both and nothing outside the
# install root is touched.

set -eu

BINARY_SRC="${1:-./pscluster}"
ROOT="${PSCLUSTER_ROOT:-}"

if [ ! -x "$BINARY_SRC" ]; then
    echo "install: $BINARY_SRC not found. Run 'make build' first." >&2
    exit 1
fi

if [ -z "$ROOT" ]; then
    if [ "$(id -u)" -eq 0 ]; then
        ROOT=/opt/plainshow-cluster
    else
        ROOT="$HOME/.plainshow-cluster"
    fi
fi

echo
echo "  Plainshow Cluster"
echo
echo "  Install root   $ROOT"
echo "  Binary         $ROOT/bin/pscluster"
echo

mkdir -p "$ROOT/bin"
cp "$BINARY_SRC" "$ROOT/bin/pscluster"
chmod 755 "$ROOT/bin/pscluster"

if [ ! -f "$ROOT/config.yaml" ]; then
    "$ROOT/bin/pscluster" init --root "$ROOT"
else
    echo "  A node already exists here — the binary was updated in place."
    echo
fi

# Optional: put the command on PATH.
LINK_DIR=""
if [ "$(id -u)" -eq 0 ] && [ -d /usr/local/bin ]; then
    LINK_DIR=/usr/local/bin
elif [ -d "$HOME/.local/bin" ]; then
    LINK_DIR="$HOME/.local/bin"
fi

if [ -n "$LINK_DIR" ]; then
    ln -sf "$ROOT/bin/pscluster" "$LINK_DIR/pscluster"
    echo "  Linked         $LINK_DIR/pscluster -> $ROOT/bin/pscluster"
fi

# Optional: a systemd unit, written into the root and linked from systemd so the
# unit file itself is still part of the install directory.
if [ "$(id -u)" -eq 0 ] && [ -d /etc/systemd/system ]; then
    cat > "$ROOT/plainshow-cluster.service" <<UNIT
[Unit]
Description=Plainshow Cluster node
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=$ROOT/bin/pscluster serve --root $ROOT
Restart=on-failure
RestartSec=3
# The node writes only inside its own root.
ReadWritePaths=$ROOT
ProtectSystem=full
NoNewPrivileges=yes

[Install]
WantedBy=multi-user.target
UNIT
    ln -sf "$ROOT/plainshow-cluster.service" /etc/systemd/system/plainshow-cluster.service
    systemctl daemon-reload 2>/dev/null || true
    echo "  Service        plainshow-cluster.service (not started)"
    echo
    echo "  Start on boot: systemctl enable --now plainshow-cluster"
fi

echo
echo "  Start it now:  pscluster serve --root $ROOT"
echo
