#!/usr/bin/env bash
# Point marooned cluster-sync / functest at an existing KubeVirt kubevirtci
# cluster (the one you start from ~/devel/kubevirt with make cluster-up).
#
#   export KUBEVIRT_DIR=$HOME/devel/kubevirt
#   export KUBEVIRT_PROVIDER=k8s-1.30   # must match the kubevirt cluster-up
#   export KUBEVIRT_MEMORY_SIZE=9216M   # only needed for kubevirt cluster-up
#
# If KUBEVIRT_DIR is unset and $HOME/devel/kubevirt/cluster-up exists, that
# tree is used. Leave KUBEVIRT_DIR empty and use this repo's cluster-up/ to
# bring up a standalone (older) cluster.

if [ -z "${KUBEVIRT_DIR:-}" ] && [ -d "${HOME}/devel/kubevirt/cluster-up" ]; then
    KUBEVIRT_DIR="${HOME}/devel/kubevirt"
fi

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
    # Fall back to the kubevirtci copy vendored in this repo.
    _here="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
    export KUBEVIRTCI_PATH="${KUBEVIRTCI_PATH:-${_here}/cluster-up}"
    export KUBEVIRTCI_CONFIG_PATH="${KUBEVIRTCI_CONFIG_PATH:-${_here}/_ci-configs}"
    export KUBEVIRTCI_CLUSTER_PATH="${KUBEVIRTCI_CLUSTER_PATH:-${KUBEVIRTCI_PATH}/cluster}"
fi

if [ -n "${KUBEVIRT_PROVIDER:-}" ] && [ ! -d "${KUBEVIRTCI_CLUSTER_PATH}/${KUBEVIRT_PROVIDER}" ]; then
    echo "Provider ${KUBEVIRT_PROVIDER} not found in ${KUBEVIRTCI_CLUSTER_PATH}" >&2
    echo "Providers in this cluster-up:" >&2
    ls -1 "${KUBEVIRTCI_CLUSTER_PATH}" | grep '^k8s-' >&2 || true
    echo "Use the same KUBEVIRT_PROVIDER you passed to kubevirt's make cluster-up." >&2
    exit 1
fi
