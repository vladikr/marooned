#!/usr/bin/env bash
# One path after cluster-up: operator + images (including guest disks) + CRI-O handler.
#
#   make cluster-up
#   make cluster-dev
#   ./cluster-up/kubectl.sh apply -f examples/sandbox-pod.yaml
#   ./cluster-up/kubectl.sh get vmi
set -eo pipefail
root="$(cd "$(dirname "$0")/.." && pwd -P)"
cd "$root"
export SKIP_GUEST_DISK=0

echo "==> cluster-sync (CRDs + operator + config)"
make cluster-sync

echo "==> cluster-push (registry images + guest disks + config patch)"
./hack/cluster-push.sh

echo "==> CRI-O marooned handler"
./hack/kubevirtci-install-crio-handler.sh

echo
echo "cluster-dev finished. Sandbox guest is the VMI, not Pod Ready:"
echo "  ./cluster-up/kubectl.sh apply -f examples/sandbox-pod.yaml"
echo "  ./cluster-up/kubectl.sh get vmi,pod"
