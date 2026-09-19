#!/usr/bin/env bash
# Install marooned onto the current cluster (kubevirtci or external).
#
# kubevirtci:
#   make cluster-up && make cluster-dev
#
# real cluster (KubeVirt already running):
#   export KUBEVIRT_PROVIDER=external
#   export KUBECONFIG=...
#   export DOCKER_PREFIX=quay.io/<you>
#   make cluster-dev
set -eo pipefail
root="$(cd "$(dirname "$0")/.." && pwd -P)"
cd "$root"
export SKIP_GUEST_DISK=0
export CACHEBUST="${CACHEBUST:-$(date +%s)}"
export PULL_POLICY="${PULL_POLICY:-Always}"
provider="${KUBEVIRT_PROVIDER:-k8s-1.37}"
echo "CACHEBUST=${CACHEBUST} PULL_POLICY=${PULL_POLICY} KUBEVIRT_MEMORY_SIZE=${KUBEVIRT_MEMORY_SIZE:-5120M}"
if [ "${KUBEVIRT_MEMORY_SIZE:-5120M}" = "5120M" ] && [ "$provider" != "external" ]; then
  echo "NOTE: kubevirtci default node RAM is 5Gi. KubeVirt + a 512Mi guest is tight."
  echo "      Rebuild the cluster with:  export KUBEVIRT_MEMORY_SIZE=16384M"
fi

if [ "$provider" != "external" ]; then
  echo "==> vhost-vsock + KubeVirt VSOCK gate"
  ./hack/ensure-vsock.sh
fi

echo "==> cluster-sync (CRDs + operator + config)"
make cluster-sync

echo "==> cluster-push (images + guest disks + config patch)"
./hack/cluster-push.sh

if [ "$provider" = "external" ]; then
  echo "==> CRI-O handler: shim DaemonSet writes /etc/crio/crio.conf.d/20-marooned.conf"
  echo "    Restart crio on workers if the handler is not visible yet."
else
  echo "==> CRI-O marooned handler (kubevirtci ssh fallback)"
  ./hack/kubevirtci-install-crio-handler.sh
fi

echo
echo "cluster-dev finished. Sandbox guest is the VMI, not Pod Ready:"
if [ "$provider" = "external" ]; then
  echo "  kubectl apply -f examples/sandbox-pod.yaml"
  echo "  kubectl get vmi,pod"
else
  echo "  ./cluster-up/kubectl.sh apply -f examples/sandbox-pod.yaml"
  echo "  ./cluster-up/kubectl.sh get vmi,pod"
fi
