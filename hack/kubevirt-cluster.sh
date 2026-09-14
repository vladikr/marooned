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

if [ -n "${KUBEVIRT_DIR:-}" ]; then
    if [ ! -d "${KUBEVIRT_DIR}/cluster-up" ]; then
        echo "KUBEVIRT_DIR=${KUBEVIRT_DIR} has no cluster-up/" >&2
        exit 1
    fi
    export KUBEVIRT_DIR
    export KUBEVIRTCI_PATH="${KUBEVIRTCI_PATH:-${KUBEVIRT_DIR}/cluster-up}"
    export KUBEVIRTCI_CONFIG_PATH="${KUBEVIRTCI_CONFIG_PATH:-${KUBEVIRT_DIR}/_ci-configs}"
    export KUBEVIRTCI_CLUSTER_PATH="${KUBEVIRTCI_CLUSTER_PATH:-${KUBEVIRTCI_PATH}/cluster}"
    echo "Using KubeVirt cluster at ${KUBEVIRT_DIR} (KUBEVIRT_PROVIDER=${KUBEVIRT_PROVIDER:-from kubevirtci default})"
else
    _here="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
    export KUBEVIRTCI_PATH="${KUBEVIRTCI_PATH:-${_here}/cluster-up}"
    export KUBEVIRTCI_CONFIG_PATH="${KUBEVIRTCI_CONFIG_PATH:-${_here}/_ci-configs}"
    export KUBEVIRTCI_CLUSTER_PATH="${KUBEVIRTCI_CLUSTER_PATH:-${KUBEVIRTCI_PATH}/cluster}"
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
