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

echo "==> CRI-O handler marooned (shim DaemonSet is the install path)"
if [ "$provider" = "external" ]; then
  echo "    Wait for marooned-shim to write /etc/crio/crio.conf.d/20-marooned.conf."
  echo "    Force-repair on a node: restart crio after the drop-in exists."
else
  K="./cluster-up/kubectl.sh"
  $K rollout status ds/marooned-shim -n marooned-system --timeout=180s 2>/dev/null || true
  ssh="./cluster-up/ssh.sh"
  loaded=0
  if $ssh node01 "sudo grep -q 'crio.runtime.runtimes.marooned' /etc/crio/crio.conf.d/20-marooned.conf 2>/dev/null && sudo crio config 2>/dev/null | grep -q 'crio.runtime.runtimes.marooned'"; then
    loaded=1
  fi
  if [ "$loaded" = 1 ]; then
    echo "    handler marooned is loaded on node01"
  else
    echo "    handler missing after shim rollout; force-repair"
    ./hack/kubevirtci-install-crio-handler.sh
  fi
  if ! $K get apiservice v1beta1.metrics.k8s.io >/dev/null 2>&1; then
    echo "==> metrics.k8s.io from guest stats (kubectl top)"
    $K apply -f examples/sandbox-metrics-apiservice.yaml || true
  fi
fi

echo
echo "cluster-dev finished. one-pod: qemu is the pause (no VMI)."
if [ "$provider" = "external" ]; then
  echo "  kubectl apply -f examples/sandbox-pod.yaml"
  echo "  kubectl get pod"
else
  echo "  ./cluster-up/kubectl.sh apply -f examples/sandbox-pod.yaml"
  echo "  ./cluster-up/kubectl.sh get pod"
fi
