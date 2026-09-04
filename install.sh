#!/bin/sh
# Install one Plainshow Cluster component and its node runtime dependencies.
# Usage: ./install.sh [node|admin] [binary]
#
# Environment:
#   PSCLUSTER_SKIP_DEPENDENCIES=1  keep package management untouched
#   PSCLUSTER_TAILSCALE_AUTH_KEY=… advanced: pre-enrol without PlainShow login
#   PSCLUSTER_TAILSCALE_LOGIN_SERVER=https://… advanced control-server override
#   PSCLUSTER_NO_START=1           install but do not enable/start services

set -eu

PSCLUSTER_TAILSCALE_LOGIN_SERVER="${PSCLUSTER_TAILSCALE_LOGIN_SERVER:-https://tailnet.plainshow.se}"

note() {
    printf '%s\n' "install: $*"
}

missing_node_commands() {
    missing=""
    command -v git >/dev/null 2>&1 || missing="$missing git"
    command -v tailscale >/dev/null 2>&1 || missing="$missing tailscale"
    command -v script >/dev/null 2>&1 || missing="$missing script"
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

# install_ray puts Ray in a virtual environment inside the node's own root.
#
# Ray is not packaged by the distributions, and modern ones refuse a system-wide
# pip install. A venv beside everything else the node owns keeps the one-root
# rule and leaves the machine's Python untouched.
install_ray() {
    root="$1"
    if [ -x "$root/runtime/bin/ray" ]; then
        note "Ray is already installed for this node"
        return 0
    fi
    note "installing Ray into $root/runtime"
    if ! python3 -m venv "$root/runtime" 2>/dev/null; then
        echo "install: could not create the Python environment for Ray" >&2
        echo "install: install python3-venv and re-run, or install Ray yourself" >&2
        return 0
    fi
    if ! "$root/runtime/bin/pip" install --quiet --upgrade pip 2>/dev/null ||
       ! "$root/runtime/bin/pip" install --quiet "ray[default]"; then
        echo "install: Ray could not be installed automatically" >&2
        echo "install: run  $root/runtime/bin/pip install 'ray[default]'" >&2
        return 0
    fi
    note "Ray installed"
}

install_apt_dependencies() {
    note "installing Debian/Ubuntu runtime dependencies"
    export DEBIAN_FRONTEND=noninteractive
    apt-get update
    apt-get install -y ca-certificates curl git util-linux python3 python3-venv python3-pip
    configure_tailscale_apt_repository
    apt-get update
    apt-get install -y tailscale
}

configure_tailscale_dnf_repository() {
    if dnf --quiet list --available tailscale >/dev/null 2>&1; then
        return
    fi
    # shellcheck disable=SC1091
    . /etc/os-release
    major="${VERSION_ID%%.*}"
    case "${ID:-}" in
        fedora) repository="https://pkgs.tailscale.com/stable/fedora/tailscale.repo" ;;
        rhel|centos|ol) repository="https://pkgs.tailscale.com/stable/${ID}/$major/tailscale.repo" ;;
        rocky|almalinux) repository="https://pkgs.tailscale.com/stable/rhel/$major/tailscale.repo" ;;
        *)
            echo "install: no automatic Tailscale repository mapping for ${ID:-this dnf system}" >&2
            exit 1
            ;;
    esac
    case "$major" in *[!0-9]*) echo "install: invalid RPM release version" >&2; exit 1 ;; esac
    note "adding Tailscale's signed RPM repository"
    install -d -m 0755 /etc/yum.repos.d
    temporary_repo="$(mktemp)"
    if ! curl -fsSL "$repository" -o "$temporary_repo" ||
       ! grep -q 'pkgs.tailscale.com' "$temporary_repo"; then
        rm -f "$temporary_repo"
        echo "install: Tailscale repository metadata failed validation" >&2
        exit 1
    fi
    install -m 0644 "$temporary_repo" /etc/yum.repos.d/tailscale.repo
    rm -f "$temporary_repo"
}

install_dnf_dependencies() {
    note "installing Fedora/RHEL runtime dependencies"
    dnf install -y ca-certificates curl git util-linux python3 python3-pip
    configure_tailscale_dnf_repository
    dnf install -y tailscale
}

configure_tailscale_zypper_repository() {
    if zypper --non-interactive search --installed-only --match-exact tailscale 2>/dev/null |
        grep -q 'tailscale'; then
        return
    fi
    if zypper --non-interactive repos tailscale-stable >/dev/null 2>&1; then
        return
    fi
    # shellcheck disable=SC1091
    . /etc/os-release
    case "${ID:-}" in
        opensuse-tumbleweed) track=tumbleweed ;;
        opensuse-leap|opensuse)
            track="leap/${VERSION_ID:-}"
            ;;
        *) echo "install: unsupported zypper distribution: ${ID:-unknown}" >&2; exit 1 ;;
    esac
    case "$track" in *[!a-zA-Z0-9./_-]*) echo "install: invalid openSUSE release" >&2; exit 1 ;; esac
    note "adding Tailscale's signed openSUSE repository"
    zypper --non-interactive addrepo --gpgcheck --refresh \
        "https://pkgs.tailscale.com/stable/opensuse/$track/tailscale.repo" tailscale-stable
}

install_zypper_dependencies() {
    note "installing openSUSE runtime dependencies"
    zypper --non-interactive install ca-certificates curl git util-linux python3 python3-pip
    configure_tailscale_zypper_repository
    zypper --non-interactive refresh
    zypper --non-interactive install tailscale
}

# install_desktop_entry makes Plainshow Cluster appear in the desktop's
# application list. It is skipped for an unprivileged or headless install.
install_desktop_entry() {
    [ "$(id -u)" -eq 0 ] || return 0
    [ -d /usr/share/applications ] || return 0
    source_dir="$(dirname "$0")/packaging/desktop"
    [ -f "$source_dir/plainshow-cluster.desktop" ] || return 0
    note "adding the desktop entry"
    install -m 0644 "$source_dir/plainshow-cluster.desktop" \
        /usr/share/applications/plainshow-cluster.desktop
    install -d -m 0755 /usr/share/icons/hicolor/scalable/apps
    install -m 0644 "$source_dir/plainshow-cluster.svg" \
        /usr/share/icons/hicolor/scalable/apps/plainshow-cluster.svg
    command -v update-desktop-database >/dev/null 2>&1 &&
        update-desktop-database /usr/share/applications 2>/dev/null || true
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
            ca-certificates git tailscale util-linux python python-pip
    elif command -v apt-get >/dev/null 2>&1 && command -v apt-cache >/dev/null 2>&1; then
        install_apt_dependencies
    elif command -v dnf >/dev/null 2>&1; then
        install_dnf_dependencies
    elif command -v zypper >/dev/null 2>&1; then
        install_zypper_dependencies
    else
        echo "install: automatic dependencies support Arch, Debian/Ubuntu, Fedora/RHEL and openSUSE" >&2
        echo "install: missing:$missing" >&2
        echo "install: install Git, Tailscale, util-linux/script and Python, or set" >&2
        echo "install: PSCLUSTER_SKIP_DEPENDENCIES=1 to install only the binary" >&2
        exit 1
    fi
    missing="$(missing_core_commands)"
    if [ -n "$missing" ]; then
        echo "install: package installation completed but commands are still missing:$missing" >&2
        exit 1
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
		note "connecting the private network with the supplied advanced authentication key"
		tailscale up --reset --auth-key="$PSCLUSTER_TAILSCALE_AUTH_KEY" \
			--login-server="$PSCLUSTER_TAILSCALE_LOGIN_SERVER" --accept-dns=false
		unset PSCLUSTER_TAILSCALE_AUTH_KEY
	else
		note "private networking is ready and will connect automatically after PlainShow login"
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
if [ "$COMPONENT" = node ]; then
    install_ray "$ROOT"
    install_desktop_entry
fi
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
