#!/usr/bin/env bash
# Default: this repo's vendored kubevirtci (independent cluster).
#
# Optional: attach to a cluster already brought up from a KubeVirt checkout
# (compat lane against unreleased virt-handler, not the default loop):
#
#   export KUBEVIRT_DIR=$HOME/devel/kubevirt
#   export KUBEVIRT_PROVIDER=k8s-1.30   # must match that cluster-up
#   make cluster-sync && make functest
#
# Do not auto-detect ~/devel/kubevirt. If that tree exists, using it by
# default would steal KUBEVIRT_PROVIDER, skip this repo's cluster-up, and
# test against whatever KubeVirt HEAD happens to be synced.

# podman-docker: DOCKER_HOST in bashrc is not enough. kubevirtci bind-mounts
# KUBEVIRTCI_PODMAN_SOCKET (default /run/podman/podman.sock, rootful). Use the
# same rootless socket as DOCKER_HOST=unix://$XDG_RUNTIME_DIR/podman/podman.sock.
if [ -z "${KUBEVIRTCI_PODMAN_SOCKET:-}" ] && [ -n "${XDG_RUNTIME_DIR:-}" ] && [ -S "${XDG_RUNTIME_DIR}/podman/podman.sock" ]; then
    export KUBEVIRTCI_PODMAN_SOCKET="${XDG_RUNTIME_DIR}/podman/podman.sock"
    export KUBEVIRTCI_RUNTIME="${KUBEVIRTCI_RUNTIME:-podman}"
fi
if [ -z "${DOCKER_HOST:-}" ] && [ -n "${XDG_RUNTIME_DIR:-}" ] && [ -S "${XDG_RUNTIME_DIR}/podman/podman.sock" ]; then
    export DOCKER_HOST="unix://${XDG_RUNTIME_DIR}/podman/podman.sock"
fi

if [ -n "${KUBEVIRT_DIR:-}" ]; then
    if [ ! -d "${KUBEVIRT_DIR}/cluster-up" ]; then
        echo "KUBEVIRT_DIR=${KUBEVIRT_DIR} has no cluster-up/" >&2
        exit 1
    fi
    export KUBEVIRT_DIR
    export KUBEVIRTCI_PATH="${KUBEVIRTCI_PATH:-${KUBEVIRT_DIR}/cluster-up}"
    # kubevirtci concatenates ${KUBEVIRTCI_PATH}hack/common.sh (no extra slash).
    case "${KUBEVIRTCI_PATH}" in
    */) ;;
    *) export KUBEVIRTCI_PATH="${KUBEVIRTCI_PATH}/" ;;
    esac
    export KUBEVIRTCI_CONFIG_PATH="${KUBEVIRTCI_CONFIG_PATH:-${KUBEVIRT_DIR}/_ci-configs}"
    export KUBEVIRTCI_CLUSTER_PATH="${KUBEVIRTCI_CLUSTER_PATH:-${KUBEVIRTCI_PATH}cluster}"
    echo "Using KubeVirt cluster at ${KUBEVIRT_DIR} (KUBEVIRT_PROVIDER=${KUBEVIRT_PROVIDER:-from kubevirtci default})"
else
    _here="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
    export KUBEVIRTCI_PATH="${KUBEVIRTCI_PATH:-${_here}/cluster-up}"
    case "${KUBEVIRTCI_PATH}" in
    */) ;;
    *) export KUBEVIRTCI_PATH="${KUBEVIRTCI_PATH}/" ;;
    esac
    export KUBEVIRTCI_CONFIG_PATH="${KUBEVIRTCI_CONFIG_PATH:-${_here}/_ci-configs}"
    export KUBEVIRTCI_CLUSTER_PATH="${KUBEVIRTCI_CLUSTER_PATH:-${KUBEVIRTCI_PATH}cluster}"
fi

if [ -n "${KUBEVIRT_PROVIDER:-}" ] && [ ! -d "${KUBEVIRTCI_CLUSTER_PATH}/${KUBEVIRT_PROVIDER}" ]; then
    echo "Provider ${KUBEVIRT_PROVIDER} not found in ${KUBEVIRTCI_CLUSTER_PATH}" >&2
    echo "Providers in this cluster-up:" >&2
    ls -1 "${KUBEVIRTCI_CLUSTER_PATH}" | grep '^k8s-' >&2 || true
    if [ -n "${KUBEVIRT_DIR:-}" ]; then
        echo "KUBEVIRT_PROVIDER must match the provider used for kubevirt's make cluster-up." >&2
    else
        echo "Bump kubevirtci (hack/update-kubevirtci.sh) or pick a provider listed above." >&2
    fi
    exit 1
fi

# kubevirtci dnsmasq.sh (set -e) uses iptables-legacy. Rootless podman is
# privileged in name only: modprobe inside the container returns EPERM, the
# container exits, and gocli hangs on "waiting for node to come up".
# Load the table modules on the host first (nf_tables is not enough).
ensure_host_iptables_modules() {
    local mods="ip_tables iptable_nat iptable_filter ip6_tables ip6table_nat ip6table_filter xt_conntrack xt_MASQUERADE"
    local missing="" m
    for m in $mods; do
        grep -q "^${m} " /proc/modules || missing="$missing $m"
    done
    missing="${missing# }"
    if [ -z "$missing" ]; then
        return 0
    fi
    echo "kubevirtci dnsmasq needs legacy iptables modules on the host."
    echo "Rootless podman cannot modprobe them (Operation not permitted)."
    echo "Missing: $missing"
    if sudo -n true 2>/dev/null; then
        # shellcheck disable=SC2086
        sudo -n modprobe $missing || true
        missing=""
        for m in $mods; do
            grep -q "^${m} " /proc/modules || missing="$missing $m"
        done
        missing="${missing# }"
        if [ -z "$missing" ]; then
            echo "Loaded host iptables modules."
            return 0
        fi
    fi
    echo "Load them, then retry cluster-up:" >&2
    echo "  sudo modprobe $missing" >&2
    echo "To persist across reboot:" >&2
    echo "  printf '%s\\n' $mods | sudo tee /etc/modules-load.d/kubevirtci.conf" >&2
    return 1
}

# virt-handler only advertises devices.kubevirt.io/vhost-vsock when the
# VSOCK feature gate is on and /dev/vhost-vsock exists on the node.
ensure_host_vsock() {
    if [ -e /dev/vhost-vsock ]; then
        return 0
    fi
    local mods="vsock vhost_vsock"
    echo "Host /dev/vhost-vsock is missing (needed for nested virtio-vsock)."
    if sudo -n true 2>/dev/null; then
        # shellcheck disable=SC2086
        sudo -n modprobe $mods || true
        if [ -e /dev/vhost-vsock ]; then
            echo "Loaded host vhost_vsock."
            return 0
        fi
    fi
    echo "Load vhost_vsock, then retry:" >&2
    echo "  sudo modprobe vsock vhost_vsock" >&2
    return 1
}
