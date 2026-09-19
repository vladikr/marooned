#!/usr/bin/env bash
# Make KubeVirt sandbox VMs schedulable with autoattachVSOCK:
#   1. vhost_vsock on kubevirtci nodes (virt-handler device plugin)
#   2. VSOCK feature gate on the KubeVirt CR
#   3. wait until nodes advertise devices.kubevirt.io/vhost-vsock
set -eo pipefail
root="$(cd "$(dirname "$0")/.." && pwd -P)"
cd "$root"
provider="${KUBEVIRT_PROVIDER:-k8s-1.37}"

if [ "$provider" = "external" ]; then
  K="${K:-kubectl}"
else
  # shellcheck source=hack/kubevirt-cluster.sh
  source ./hack/kubevirt-cluster.sh
  K="./cluster-up/kubectl.sh"
fi

echo "==> vhost_vsock on nodes"
if [ "$provider" != "external" ]; then
  nodes="${KUBEVIRT_NUM_NODES:-1}"
  for i in $(seq 1 "$nodes"); do
    node=$(printf 'node%02d' "$i")
    ./cluster-up/ssh.sh "$node" "sudo modprobe vsock vhost_vsock; test -e /dev/vhost-vsock"
  done
fi

echo "==> KubeVirt VSOCK feature gate"
if ! $K get kubevirt kubevirt -n kubevirt >/dev/null 2>&1; then
  echo "KubeVirt CR not found; skip gate" >&2
  exit 0
fi
gates="$($K get kubevirt kubevirt -n kubevirt -o jsonpath='{.spec.configuration.developerConfiguration.featureGates[*]}' 2>/dev/null || true)"
if echo " $gates " | grep -qw VSOCK; then
  echo "VSOCK already enabled"
else
  if $K get kubevirt kubevirt -n kubevirt -o jsonpath='{.spec.configuration.developerConfiguration.featureGates}' | grep -q '\[' 2>/dev/null; then
    $K patch kubevirt kubevirt -n kubevirt --type json \
      -p '[{"op":"add","path":"/spec/configuration/developerConfiguration/featureGates/-","value":"VSOCK"}]'
  else
    $K patch kubevirt kubevirt -n kubevirt --type merge \
      -p '{"spec":{"configuration":{"developerConfiguration":{"featureGates":["VSOCK"]}}}}'
  fi
fi

echo "==> wait for devices.kubevirt.io/vhost-vsock allocatable"
ok=0
for i in $(seq 1 36); do
  alloc="$($K get nodes -o jsonpath='{range .items[*]}{.status.allocatable.devices\.kubevirt\.io/vhost-vsock}{" "}{end}' 2>/dev/null || true)"
  if echo "$alloc" | grep -Eq '[1-9]'; then
    echo "vhost-vsock allocatable: $alloc"
    ok=1
    break
  fi
  if [ "$i" = 3 ] && [ "$provider" != "external" ]; then
    echo "raising inotify limits and bouncing virt-handler"
    nodes="${KUBEVIRT_NUM_NODES:-1}"
    for n in $(seq 1 "$nodes"); do
      node=$(printf 'node%02d' "$n")
      ./cluster-up/ssh.sh "$node" "sudo sysctl -w fs.inotify.max_user_instances=1024 fs.inotify.max_user_watches=1048576 >/dev/null || true"
    done
    $K rollout restart ds/virt-handler -n kubevirt 2>/dev/null || true
    $K rollout status ds/virt-handler -n kubevirt --timeout=180s 2>/dev/null || true
  fi
  sleep 5
done
if [ "$ok" != 1 ]; then
  echo "ERROR: nodes have no devices.kubevirt.io/vhost-vsock. Check /dev/vhost-vsock and virt-handler logs." >&2
  $K get nodes -o jsonpath='{.items[*].status.allocatable}' >&2 || true
  exit 1
fi

echo "==> wait for kubevirt.io/schedulable=true"
ok=0
for _ in $(seq 1 36); do
  sch="$($K get nodes -o jsonpath='{range .items[*]}{.metadata.labels.kubevirt\.io/schedulable}{" "}{end}' 2>/dev/null || true)"
  if echo " $sch " | grep -q ' true '; then
    echo "schedulable: $sch"
    ok=1
    break
  fi
  sleep 5
done
if [ "$ok" != 1 ]; then
  echo "ERROR: no node is kubevirt.io/schedulable=true (virt-handler heartbeat)." >&2
  $K get nodes --show-labels | grep kubevirt.io/schedulable >&2 || true
  exit 1
fi
