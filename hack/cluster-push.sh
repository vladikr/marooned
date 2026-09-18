#!/usr/bin/env bash
# Rebuild and push images into the running kubevirtci registry, then bounce
# the controller and shim. Guest disks are included unless SKIP_GUEST_DISK=1.
#
#   export KUBEVIRT_PROVIDER=k8s-1.37
#   ./hack/cluster-push.sh
#
# Then, once, on the nodes:
#   ./hack/kubevirtci-install-crio-handler.sh
set -eo pipefail
root="$(cd "$(dirname "$0")/.." && pwd -P)"
cd "$root"
# kubevirtci common.sh assigns KUBEVIRT_CUSTOM_AAQ_VERSION=${KUBEVIRT_CUSTOM_AAQ_VERSION}
# (no default). nounset would abort there; cluster-sync does not use -u either.
# shellcheck source=hack/kubevirt-cluster.sh
source ./hack/kubevirt-cluster.sh
source ./hack/build/config.sh
source "${KUBEVIRTCI_PATH}/hack/common.sh"
source "${KUBEVIRTCI_CLUSTER_PATH}/${KUBEVIRT_PROVIDER}/provider.sh"

registry="localhost:$(_port registry)"
export DOCKER_PREFIX="${DOCKER_PREFIX:-$registry}"
export DOCKER_TAG="${DOCKER_TAG:-latest}"
export PUSH_TARGETS="${PUSH_TARGETS:-${CONTROLLER_IMAGE_NAME} ${MAROONEDPODS_SERVER_IMAGE_NAME} ${OPERATOR_IMAGE_NAME} ${SHIM_IMAGE_NAME}}"

echo "DOCKER_PREFIX=${DOCKER_PREFIX}  PUSH_TARGETS=${PUSH_TARGETS}  SKIP_GUEST_DISK=${SKIP_GUEST_DISK:-0}"
if [ "${SKIP_GUEST_DISK:-0}" = "1" ]; then
  echo "WARNING: SKIP_GUEST_DISK=1 — virt-launcher will ImagePullBackOff (manifest unknown)." >&2
fi

./hack/build/build-docker.sh build
./hack/build/build-docker.sh push

verify_registry_repo() {
  local name="$1"
  local catalog
  catalog="$(curl -sf "http://${registry}/v2/_catalog" || true)"
  if ! echo "$catalog" | grep -q "\"${name}\""; then
    echo "ERROR: ${name} not in ${registry} catalog: ${catalog}" >&2
    exit 1
  fi
  echo "registry has ${name}: $(curl -sf "http://${registry}/v2/${name}/tags/list")"
}

if [ "${SKIP_GUEST_DISK:-0}" != "1" ]; then
  ./hack/build/build-sandbox-disk.sh
  cri=podman
  command -v podman >/dev/null || cri=docker
  insecure=()
  if [ "$cri" = podman ]; then
    insecure=(--tls-verify=false)
  fi
  $cri build -t "${DOCKER_PREFIX}/${SANDBOX_IMAGE_NAME}:${DOCKER_TAG}" \
    -f _out/sandbox-disk/Dockerfile.sandbox _out/sandbox-disk
  $cri build -t "${DOCKER_PREFIX}/${KERNEL_IMAGE_NAME}:${DOCKER_TAG}" \
    -f _out/sandbox-disk/Dockerfile.kernel _out/sandbox-disk
  $cri push "${insecure[@]}" "${DOCKER_PREFIX}/${SANDBOX_IMAGE_NAME}:${DOCKER_TAG}"
  $cri push "${insecure[@]}" "${DOCKER_PREFIX}/${KERNEL_IMAGE_NAME}:${DOCKER_TAG}"
  verify_registry_repo "${SANDBOX_IMAGE_NAME}"
  verify_registry_repo "${KERNEL_IMAGE_NAME}"
fi

K="./cluster-up/kubectl.sh"
if [ -x "$K" ]; then
  if ! $K get crd maroonedpodsconfigs.maroonedpods.io >/dev/null 2>&1; then
    echo "ERROR: MaroonedPods is not installed (no maroonedpodsconfigs CRD)." >&2
    echo "Images are in ${DOCKER_PREFIX}. Install next:" >&2
    echo "  make cluster-sync && make cluster-push && ./hack/kubevirtci-install-crio-handler.sh" >&2
    echo "Or: make cluster-dev" >&2
    exit 1
  fi

  echo "restarting operator, controller, and shim"
  $K rollout restart deploy/maroonedpods-operator -n maroonedpods 2>/dev/null || true
  $K rollout restart deploy/maroonedpods-controller -n maroonedpods 2>/dev/null || true
  $K rollout restart ds/marooned-shim -n marooned-system 2>/dev/null || true
  $K rollout status deploy/maroonedpods-operator -n maroonedpods --timeout=180s 2>/dev/null || true
  $K rollout status deploy/maroonedpods-controller -n maroonedpods --timeout=180s 2>/dev/null || true
  $K rollout status ds/marooned-shim -n marooned-system --timeout=180s 2>/dev/null || true

  if [ "${SKIP_GUEST_DISK:-0}" != "1" ]; then
    echo "patching MaroonedPodsConfig default to registry:5000 guest images"
    if ! $K get maroonedpodsconfig default >/dev/null 2>&1; then
      echo "MaroonedPodsConfig default missing; applying examples/maroonedpods-config.yaml"
      $K apply -f examples/maroonedpods-config.yaml
    fi
    $K patch maroonedpodsconfig default --type merge -p "{
      \"spec\": {
        \"sandbox\": {
          \"rootfsImage\": \"registry:5000/${SANDBOX_IMAGE_NAME}:${DOCKER_TAG}\",
          \"kernelBoot\": {
            \"image\": \"registry:5000/${KERNEL_IMAGE_NAME}:${DOCKER_TAG}\",
            \"kernelPath\": \"/boot/vmlinuz\",
            \"initrdPath\": \"/boot/initrd\",
            \"kernelArgs\": \"root=/dev/vda rootfstype=ext4 rw console=ttyS0\"
          }
        }
      }
    }"
  fi
fi

echo
echo "cluster-push finished."
echo "If kubelet still says 'failed to find runtime handler marooned', once:"
echo "  ./hack/kubevirtci-install-crio-handler.sh"
