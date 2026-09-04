#!/bin/sh
# Install one Plainshow Cluster component without installing dependencies.
# Usage: ./install.sh [node|admin] [binary]

set -eu

COMPONENT="${1:-node}"
case "$COMPONENT" in
    node)       BINARY_NAME=pscluster; ROOT="${PSCLUSTER_ROOT:-/opt/plainshow-cluster}" ;;
    admin)      BINARY_NAME=pscluster-admin; ROOT="${PSCLUSTER_ADMIN_ROOT:-/opt/plainshow-cluster-admin}" ;;
    *) echo "install: component must be node or admin" >&2; exit 2 ;;
esac

BINARY_SRC="${2:-./$BINARY_NAME}"
if [ ! -x "$BINARY_SRC" ]; then
    echo "install: $BINARY_SRC not found; run 'make build' first" >&2
    exit 1
fi

if [ "$(id -u)" -ne 0 ] && [ "${ROOT#/opt/}" != "$ROOT" ]; then
    ROOT="${HOME}/.${BINARY_NAME}"
fi

mkdir -p "$ROOT/bin"
temporary="$ROOT/bin/.${BINARY_NAME}.new"
cp "$BINARY_SRC" "$temporary"
chmod 755 "$temporary"
mv "$temporary" "$ROOT/bin/$BINARY_NAME"

case "$COMPONENT" in
    node)
        if [ ! -f "$ROOT/config.yaml" ]; then
            set -- init --root "$ROOT"
            if [ -n "${PSCLUSTER_ACCOUNT_SERVER:-}" ]; then
                set -- "$@" --account-server "$PSCLUSTER_ACCOUNT_SERVER"
            fi
            "$ROOT/bin/$BINARY_NAME" "$@"
        fi
        ;;
    admin)
        if [ ! -f "$ROOT/admin.yaml" ]; then
            set -- init --root "$ROOT"
            if [ -n "${PSCLUSTER_ADMIN_URL:-}" ]; then
                set -- "$@" --public-url "$PSCLUSTER_ADMIN_URL"
            fi
            "$ROOT/bin/$BINARY_NAME" "$@"
        fi
        ;;
esac

if [ "$(id -u)" -eq 0 ] && [ -d /usr/local/bin ]; then
    ln -sf "$ROOT/bin/$BINARY_NAME" "/usr/local/bin/$BINARY_NAME"
fi

SERVICE_NAME="plainshow-cluster"
[ "$COMPONENT" = admin ] && SERVICE_NAME="plainshow-cluster-admin"

if [ "$(id -u)" -eq 0 ] && [ -d /etc/systemd/system ]; then
    SERVICE_FILE="$ROOT/$SERVICE_NAME.service"
    {
        echo '[Unit]'
        echo "Description=Plainshow Cluster $COMPONENT"
        echo 'After=network-online.target'
        echo 'Wants=network-online.target'
        echo
        echo '[Service]'
        echo 'Type=simple'
        echo "ExecStart=$ROOT/bin/$BINARY_NAME serve --root $ROOT"
        echo 'Restart=on-failure'
        echo 'RestartSec=3'
        echo "ReadWritePaths=$ROOT"
        echo 'ProtectSystem=full'
        echo 'PrivateTmp=yes'
        echo 'NoNewPrivileges=yes'
        echo
        echo '[Install]'
        echo 'WantedBy=multi-user.target'
    } > "$SERVICE_FILE"
    chmod 640 "$SERVICE_FILE"
    ln -sf "$SERVICE_FILE" "/etc/systemd/system/$SERVICE_NAME.service"
    systemctl daemon-reload 2>/dev/null || true
fi

echo
echo "Installed $BINARY_NAME in $ROOT"
echo "Run now: $ROOT/bin/$BINARY_NAME serve --root $ROOT"
if [ "$(id -u)" -eq 0 ] && [ -d /etc/systemd/system ]; then
    echo "Start on boot: systemctl enable --now $SERVICE_NAME"
fi
