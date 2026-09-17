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

echo "DOCKER_PREFIX=${DOCKER_PREFIX}  PUSH_TARGETS=${PUSH_TARGETS}"

./hack/build/build-docker.sh build
./hack/build/build-docker.sh push

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
fi

K="./cluster-up/kubectl.sh"
if [ -x "$K" ]; then
  echo "restarting controller and shim"
  $K rollout restart deploy/maroonedpods-controller -n maroonedpods 2>/dev/null || true
  $K rollout restart ds/marooned-shim -n marooned-system 2>/dev/null || true
  $K rollout status deploy/maroonedpods-controller -n maroonedpods --timeout=180s 2>/dev/null || true
  $K rollout status ds/marooned-shim -n marooned-system --timeout=180s 2>/dev/null || true

  if [ "${SKIP_GUEST_DISK:-0}" != "1" ]; then
    echo "patching MaroonedPodsConfig default to registry:5000 guest images"
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
    }" || echo "warning: could not patch MaroonedPodsConfig (is the CR installed?)"
  fi
fi

echo
echo "cluster-push finished."
echo "If kubelet still says 'failed to find runtime handler marooned', once:"
echo "  ./hack/kubevirtci-install-crio-handler.sh"
