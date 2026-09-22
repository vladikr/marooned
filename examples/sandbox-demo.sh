#!/usr/bin/env bash
# Recordable walkthrough of RuntimeClass=marooned on kubevirtci.
#
#   export K=./cluster-up/kubectl.sh
#   asciinema rec --title "marooned sandbox-mode" -c "bash examples/sandbox-demo.sh" demo.cast
#   asciinema play demo.cast
#   asciinema upload demo.cast   # optional
set -euo pipefail
cd "$(dirname "$0")/.."
K="${K:-./cluster-up/kubectl.sh}"
pause() { sleep "${DEMO_PAUSE:-1}"; }

say() { printf '\n\n======== %s ========\n' "$*"; pause; }

$K delete pod isolated-busybox1 isolated-nginx isolated-python --ignore-not-found --wait=false >/dev/null 2>&1 || true
$K delete svc isolated-busybox1 isolated-nginx --ignore-not-found >/dev/null 2>&1 || true
pause

say "1. RuntimeClass marooned + node shim (CRI unix socket)"
$K get runtimeclass marooned
$K -n marooned-system get pods -o wide

say "2. User Pod: only runtimeClassName differs from a normal Pod"
$K apply -f examples/sandbox-pod.yaml
$K wait --for=condition=Ready pod/isolated-busybox1 --timeout=180s
$K get pod isolated-busybox1 -o wide
$K get vmi

say "3. Hidden VMI: virt-launcher (compute) + vsockfwd sidecar"
$K get pod -l kubevirt.io=virt-launcher -o wide
$K get pod -l kubevirt.io=virt-launcher -o jsonpath='{range .spec.containers[*]}  {.name}: {.image}{"\n"}{end}'
echo

say "4. kubectl exec is the guest image (httpd), not the host pause"
$K exec isolated-busybox1 -- ps
$K exec isolated-busybox1 -- cat /scratch/ok
$K exec isolated-busybox1 -- cat /etc/os-release | head -4

say "5. Service reaches guest httpd via masquerade (not status.podIP)"
echo "  podIP    = $($K get pod isolated-busybox1 -o jsonpath='{.status.podIP}')"
echo "  guest-ip = $($K get pod isolated-busybox1 -o jsonpath='{.metadata.annotations.maroonedpods\.io/guest-ip}')"
$K get svc isolated-busybox1
$K run --rm -i --image=quay.io/prometheus/busybox:latest --restart=Never --image-pull-policy=Never demo-wget -- wget -qO- http://isolated-busybox1:8080/ || true

say "6. Guest process death is a real container exit (Always then restarts)"
$K exec isolated-busybox1 -- killall httpd || true
sleep 4
$K get pod isolated-busybox1

say "done"
echo "  RuntimeClass marooned → CRI-O marooned-oci → marooned-shim"
echo "  → VMI marooned-<pod-uid> → virt-launcher + vsockfwd → guest agent"
echo "  → user image on emptyDisk, vsock CRI, masquerade 10.0.2.2"
echo "  Kata-shaped nginx:  kubectl apply -f examples/sandbox-nginx.yaml"
