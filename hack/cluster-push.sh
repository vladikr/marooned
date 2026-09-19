#!/usr/bin/env bash
# Rebuild and push images, then bounce controller/shim and point
# MaroonedPodsConfig at those guest disks.
#
# kubevirtci (default): always push to localhost:$(_port registry) so nodes
# pull registry:5000/...  A leftover DOCKER_PREFIX=quay.io/... is ignored.
#
# external cluster:
#   export KUBEVIRT_PROVIDER=external
#   export DOCKER_PREFIX=quay.io/<you>
#   export KUBECONFIG=...
#   ./hack/cluster-push.sh
set -eo pipefail
root="$(cd "$(dirname "$0")/.." && pwd -P)"
cd "$root"
source ./hack/build/config.sh

provider="${KUBEVIRT_PROVIDER:-k8s-1.37}"
DOCKER_TAG="${DOCKER_TAG:-latest}"
K="kubectl"

if [ "$provider" = "external" ]; then
  if [ -z "${DOCKER_PREFIX:-}" ]; then
    echo "ERROR: KUBEVIRT_PROVIDER=external requires DOCKER_PREFIX (e.g. quay.io/you)" >&2
    exit 1
  fi
  guest_pull_sandbox="${DOCKER_PREFIX}/${SANDBOX_IMAGE_NAME}:${DOCKER_TAG}"
  guest_pull_kernel="${DOCKER_PREFIX}/${KERNEL_IMAGE_NAME}:${DOCKER_TAG}"
else
  # shellcheck source=hack/kubevirt-cluster.sh
  source ./hack/kubevirt-cluster.sh
  source "${KUBEVIRTCI_PATH}/hack/common.sh"
  source "${KUBEVIRTCI_CLUSTER_PATH}/${provider}/provider.sh"
  registry="localhost:$(_port registry)"
  if [ -n "${DOCKER_PREFIX:-}" ] && [ "$DOCKER_PREFIX" != "$registry" ]; then
    echo "NOTE: ignoring DOCKER_PREFIX=${DOCKER_PREFIX} on kubevirtci; pushing to ${registry}"
  fi
  export DOCKER_PREFIX="$registry"
  guest_pull_sandbox="registry:5000/${SANDBOX_IMAGE_NAME}:${DOCKER_TAG}"
  guest_pull_kernel="registry:5000/${KERNEL_IMAGE_NAME}:${DOCKER_TAG}"
  K="./cluster-up/kubectl.sh"
fi

export DOCKER_PREFIX
export DOCKER_TAG
export PUSH_TARGETS="${PUSH_TARGETS:-${CONTROLLER_IMAGE_NAME} ${MAROONEDPODS_SERVER_IMAGE_NAME} ${OPERATOR_IMAGE_NAME} ${SHIM_IMAGE_NAME}}"

echo "provider=${provider}  DOCKER_PREFIX=${DOCKER_PREFIX}  guest=${guest_pull_sandbox}  SKIP_GUEST_DISK=${SKIP_GUEST_DISK:-0}"
if [ "${SKIP_GUEST_DISK:-0}" = "1" ]; then
  echo "WARNING: SKIP_GUEST_DISK=1 — virt-launcher will ImagePullBackOff (manifest unknown)." >&2
fi

./hack/build/build-docker.sh build
./hack/build/build-docker.sh push

verify_pushed() {
  local ref="$1"
  cri=podman
  command -v podman >/dev/null || cri=docker
  if [[ "$ref" == localhost:*/* ]]; then
    local host="${ref%%/*}"
    local name="${ref#*/}"
    name="${name%%:*}"
    local catalog
    catalog="$(curl -sf "http://${host}/v2/_catalog" || true)"
    if ! echo "$catalog" | grep -q "\"${name}\""; then
      echo "ERROR: ${name} not in ${host} catalog: ${catalog}" >&2
      exit 1
    fi
    echo "registry has ${name}: $(curl -sf "http://${host}/v2/${name}/tags/list")"
    return
  fi
  if $cri manifest inspect "$ref" >/dev/null 2>&1 || $cri image exists "$ref" 2>/dev/null; then
    echo "image present: ${ref}"
    return
  fi
  echo "ERROR: cannot inspect ${ref} after push" >&2
  exit 1
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
  verify_pushed "${DOCKER_PREFIX}/${SANDBOX_IMAGE_NAME}:${DOCKER_TAG}"
  verify_pushed "${DOCKER_PREFIX}/${KERNEL_IMAGE_NAME}:${DOCKER_TAG}"
fi

if [ -x "$K" ] || command -v "$K" >/dev/null 2>&1; then
  if ! $K get crd maroonedpodsconfigs.maroonedpods.io >/dev/null 2>&1; then
    echo "ERROR: MaroonedPods is not installed (no maroonedpodsconfigs CRD)." >&2
    echo "Images are in ${DOCKER_PREFIX}. Next: make cluster-sync && make cluster-push" >&2
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
    echo "patching MaroonedPodsConfig default to ${guest_pull_sandbox}"
    if ! $K get maroonedpodsconfig default >/dev/null 2>&1; then
      $K apply -f examples/maroonedpods-config.yaml
    fi
    $K patch maroonedpodsconfig default --type merge -p "{
      \"spec\": {
        \"sandbox\": {
          \"rootfsImage\": \"${guest_pull_sandbox}\",
          \"kernelBoot\": {
            \"image\": \"${guest_pull_kernel}\",
            \"kernelPath\": \"/boot/vmlinuz\",
            \"initrdPath\": \"/boot/initrd\",
            \"kernelArgs\": \"root=/dev/vda rootfstype=ext4 rw console=ttyS0\"
          }
        }
      }
    }"
  fi

  echo "waiting for mutating webhook rules"
  for i in $(seq 1 90); do
    names="$($K get mutatingwebhookconfiguration maroonedpods-mutator -o jsonpath='{.webhooks[*].name}' 2>/dev/null || true)"
    if echo "$names" | grep -q gater; then
      echo "mutating webhook ready: ${names}"
      break
    fi
    if [ "$i" -eq 90 ]; then
      echo "ERROR: maroonedpods-mutator has no webhook rules" >&2
      $K get mutatingwebhookconfiguration maroonedpods-mutator -o yaml >&2 || true
      exit 1
    fi
    sleep 2
  done
fi

echo
echo "cluster-push finished. Guest pull spec: ${guest_pull_sandbox}"
echo "CRI-O handler: shim DaemonSet installs it on the node; kubevirtci fallback:"
echo "  ./hack/kubevirtci-install-crio-handler.sh"
