#!/usr/bin/env bash
# Recordable walkthrough of RuntimeClass=marooned on kubevirtci.
#   export K=./cluster-up/kubectl.sh
#   bash examples/sandbox-demo.sh
set -euo pipefail
cd "$(dirname "$0")/.."
K="${K:-./cluster-up/kubectl.sh}"

say() { printf '\n==== %s ====\n' "$*"; }

say "1. RuntimeClass and shim (node CRI helper)"
$K get runtimeclass marooned
$K -n marooned-system get ds,pods -o wide

say "2. Apply user Pod (busybox httpd in the guest, not on the host)"
$K apply -f examples/sandbox-pod.yaml
$K wait --for=condition=Ready pod/isolated-busybox1 --timeout=120s || true
$K get pod isolated-busybox1 -o wide
$K get vmi,pod -l maroonedpods.io/sandbox=true 2>/dev/null || $K get vmi

say "3. Hidden VMI + virt-launcher (compute, disks, vsockfwd sidecar)"
$K get pod -l kubevirt.io=virt-launcher -o wide
$K get pod -l kubevirt.io=virt-launcher -o jsonpath='{range .spec.containers[*]}{.name}{" "}{.image}{"\n"}{end}'

say "4. Guest is the user image (httpd), not the pause process"
$K exec isolated-busybox1 -- ps
$K exec isolated-busybox1 -- cat /scratch/ok
$K exec isolated-busybox1 -- cat /etc/os-release | head -5

say "5. Service hits masquerade (virt-launcher IP), not status.podIP"
echo "podIP=$($K get pod isolated-busybox1 -o jsonpath='{.status.podIP}')"
echo "guest-ip=$($K get pod isolated-busybox1 -o jsonpath='{.metadata.annotations.maroonedpods\.io/guest-ip}')"
$K get svc isolated-busybox1 endpointslice marooned-isolated-busybox1
$K run --rm -i --image=quay.io/prometheus/busybox:latest --restart=Never demo-wget -- wget -qO- http://isolated-busybox1:8080/

say "6. Guest death is visible to kubelet (restartPolicy Always)"
$K exec isolated-busybox1 -- killall httpd || true
sleep 3
$K get pod isolated-busybox1

say "7. Optional Kata-shaped workload (stock nginx, only runtimeClassName differs)"
echo "  $K apply -f examples/sandbox-nginx.yaml"
echo "  # then wget http://isolated-nginx:80/  (welcome to nginx)"

say "done. Components: RuntimeClass marooned → CRI-O marooned-oci → marooned-shim"
echo "  → adaptor VMI marooned-<pod-uid> → virt-launcher + vsockfwd → guest marooned-agent"
echo "  → user rootfs on emptyDisk, vsock CRI, masquerade 10.0.2.2"
