#!/bin/sh
# Install one Plainshow Cluster component and its node runtime dependencies.
# Usage: ./install.sh [node|admin] [binary]
#
# Environment:
#   PSCLUSTER_SKIP_DEPENDENCIES=1  keep package management untouched
#   PSCLUSTER_TAILSCALE_AUTH_KEY=… connect Tailscale without browser login
#   PSCLUSTER_TAILSCALE_LOGIN_SERVER=https://… use a Headscale server
#   PSCLUSTER_NO_START=1           install but do not enable/start services

set -eu

note() {
    printf '%s\n' "install: $*"
}

missing_node_commands() {
    missing=""
    command -v git >/dev/null 2>&1 || missing="$missing git"
    command -v tailscale >/dev/null 2>&1 || missing="$missing tailscale"
    command -v script >/dev/null 2>&1 || missing="$missing script"
    command -v jupyter-server >/dev/null 2>&1 || missing="$missing jupyter-server"
    printf '%s' "$missing"
}

missing_core_commands() {
    missing=""
    command -v git >/dev/null 2>&1 || missing="$missing git"
    command -v tailscale >/dev/null 2>&1 || missing="$missing tailscale"
    command -v script >/dev/null 2>&1 || missing="$missing script"
    printf '%s' "$missing"
}

configure_tailscale_apt_repository() {
    if apt-cache show tailscale >/dev/null 2>&1; then
        return
    fi
    if [ ! -r /etc/os-release ]; then
        echo "install: cannot identify this apt-based distribution" >&2
        exit 1
    fi
    # The values come from the root-owned distro identity file. Limit them to
    # the characters accepted by the official repository paths before use.
    # shellcheck disable=SC1091
    . /etc/os-release
    distro="${ID:-}"
    codename="${VERSION_CODENAME:-}"
    case "$distro" in
        ubuntu)
            [ -n "$codename" ] || codename="${UBUNTU_CODENAME:-}"
            ;;
        debian|raspbian)
            ;;
        *)
            case " ${ID_LIKE:-} " in
                *" ubuntu "*) distro=ubuntu; codename="${UBUNTU_CODENAME:-$codename}" ;;
                *" debian "*) distro=debian ;;
                *)
                    echo "install: unsupported apt distribution: ${ID:-unknown}" >&2
                    exit 1
                    ;;
            esac
            ;;
    esac
    case "$distro:$codename" in
        *[!a-z0-9:_-]*|:|*:) echo "install: invalid distribution codename" >&2; exit 1 ;;
    esac

    note "adding Tailscale's signed repository for $distro/$codename"
    install -d -m 0755 /usr/share/keyrings /etc/apt/sources.list.d
    temporary_key="$(mktemp)"
    temporary_list="$(mktemp)"
    base="https://pkgs.tailscale.com/stable/$distro/$codename"
    if ! curl -fsSL "$base.noarmor.gpg" -o "$temporary_key" ||
       ! curl -fsSL "$base.tailscale-keyring.list" -o "$temporary_list"; then
        rm -f "$temporary_key" "$temporary_list"
        echo "install: Tailscale does not publish packages for $distro/$codename" >&2
        exit 1
    fi
    if [ ! -s "$temporary_key" ] || ! grep -q 'pkgs.tailscale.com' "$temporary_list"; then
        rm -f "$temporary_key" "$temporary_list"
        echo "install: Tailscale repository metadata failed validation" >&2
        exit 1
    fi
    install -m 0644 "$temporary_key" /usr/share/keyrings/tailscale-archive-keyring.gpg
    install -m 0644 "$temporary_list" /etc/apt/sources.list.d/tailscale.list
    rm -f "$temporary_key" "$temporary_list"
}

install_apt_dependencies() {
    note "installing Debian/Ubuntu runtime dependencies"
    export DEBIAN_FRONTEND=noninteractive
    apt-get update
    apt-get install -y ca-certificates curl git util-linux
    configure_tailscale_apt_repository
    apt-get update
    apt-get install -y tailscale
    if apt-cache show jupyter-server >/dev/null 2>&1; then
        apt-get install -y jupyter-server
    else
        note "jupyter-server is unavailable in the enabled apt repositories; notebooks remain disabled"
    fi
}

install_node_dependencies() {
    missing="$(missing_node_commands)"
    [ -n "$missing" ] || {
        note "node dependencies are already installed"
        return
    }
    if [ "${PSCLUSTER_SKIP_DEPENDENCIES:-0}" = 1 ]; then
        note "dependency installation skipped; missing:$missing"
        return
    fi
    if [ "$(id -u)" -ne 0 ]; then
        echo "install: missing node dependencies:$missing" >&2
        echo "install: rerun with sudo, or install them before continuing" >&2
        exit 1
    fi
    if command -v pacman >/dev/null 2>&1; then
        note "installing Arch runtime dependencies"
        # Do not use -Sy here: updating only package databases can create an
        # unsupported partial Arch upgrade. --needed keeps repeat runs cheap.
        pacman -S --needed --noconfirm \
            ca-certificates git tailscale util-linux jupyter-server
    elif command -v apt-get >/dev/null 2>&1 && command -v apt-cache >/dev/null 2>&1; then
        install_apt_dependencies
    else
        echo "install: automatic dependency installation supports Arch, Debian and Ubuntu" >&2
        echo "install: missing:$missing" >&2
        echo "install: install Git, Tailscale, util-linux/script and jupyter-server, or set" >&2
        echo "install: PSCLUSTER_SKIP_DEPENDENCIES=1 to install only the binary" >&2
        exit 1
    fi
    missing="$(missing_core_commands)"
    if [ -n "$missing" ]; then
        echo "install: package installation completed but commands are still missing:$missing" >&2
        exit 1
    fi
    if ! command -v jupyter-server >/dev/null 2>&1; then
        note "jupyter-server is optional and was not found; the rest of the node is ready"
    fi
}

start_tailnet() {
    [ "${PSCLUSTER_NO_START:-0}" = 1 ] && return
    if [ "$(id -u)" -eq 0 ] && command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
        note "enabling the Tailscale daemon"
        systemctl enable --now tailscaled.service
    fi
    if tailscale status >/dev/null 2>&1; then
        note "Tailscale is connected"
        return
    fi
    if [ -n "${PSCLUSTER_TAILSCALE_AUTH_KEY:-}" ]; then
        note "connecting Tailscale with the supplied authentication key"
        if [ -n "${PSCLUSTER_TAILSCALE_LOGIN_SERVER:-}" ]; then
            tailscale up --auth-key="$PSCLUSTER_TAILSCALE_AUTH_KEY" \
                --login-server="$PSCLUSTER_TAILSCALE_LOGIN_SERVER"
        else
            tailscale up --auth-key="$PSCLUSTER_TAILSCALE_AUTH_KEY"
        fi
        unset PSCLUSTER_TAILSCALE_AUTH_KEY
    else
        note "Tailscale is installed and running, but still needs account login"
        if [ -n "${PSCLUSTER_TAILSCALE_LOGIN_SERVER:-}" ]; then
            note "run: sudo tailscale up --login-server=$PSCLUSTER_TAILSCALE_LOGIN_SERVER"
        else
            note "run: sudo tailscale up"
        fi
    fi
}

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

if [ "$COMPONENT" = node ]; then
    install_node_dependencies
    start_tailnet
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
    if [ "${PSCLUSTER_NO_START:-0}" != 1 ] && [ -d /run/systemd/system ]; then
        systemctl enable --now "$SERVICE_NAME.service"
    fi
fi

echo
echo "Installed $BINARY_NAME in $ROOT"
echo "Run now: $ROOT/bin/$BINARY_NAME serve --root $ROOT"
if [ "$(id -u)" -eq 0 ] && [ -d /etc/systemd/system ]; then
    if [ "${PSCLUSTER_NO_START:-0}" = 1 ]; then
        echo "Start on boot: systemctl enable --now $SERVICE_NAME"
    else
        echo "Service enabled and started: $SERVICE_NAME"
    fi
fi
