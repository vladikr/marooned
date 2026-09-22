#!/usr/bin/env bash
# Recordable walkthrough of RuntimeClass=marooned on kubevirtci.
#
#   export K=./cluster-up/kubectl.sh
#   asciinema record -c "bash examples/sandbox-demo.sh" demo.cast
set -euo pipefail
cd "$(dirname "$0")/.."
K="${K:-./cluster-up/kubectl.sh}"
pause() { sleep "${DEMO_PAUSE:-1}"; }
say() { printf '\n\n======== %s ========\n' "$*"; pause; }

$K delete -f examples/sandbox-pod.yaml --ignore-not-found --wait=false >/dev/null 2>&1 || true
pause

say "1. RuntimeClass marooned — the only extra field on a normal Pod"
$K get runtimeclass marooned
$K -n marooned-system get pods -o wide

say "2. kubectl apply the Pod (that is the user API)"
$K apply -f examples/sandbox-pod.yaml
echo
echo "kubectl get pods --watch  (until isolated-busybox1 is Ready)"
watch_pid=""
$K get pods --watch &
watch_pid=$!
trap 'kill "$watch_pid" 2>/dev/null || true' EXIT
$K wait --for=condition=Ready pod/isolated-busybox1 --timeout=180s
sleep 3
kill "$watch_pid" 2>/dev/null || true
wait "$watch_pid" 2>/dev/null || true
trap - EXIT
echo

say "3. Same kubectl get: user Pod + the VMI/virt-launcher marooned created for it"
$K get pod isolated-busybox1 -o wide
$K get vmi
$K get pod -l kubevirt.io=virt-launcher -o wide
echo "virt-launcher containers:"
$K get pod -l kubevirt.io=virt-launcher -o jsonpath='{range .spec.containers[*]}  {.name}: {.image}{"\n"}{end}'
echo
echo "You still only exec/logs/delete the Pod. The VMI is the isolation backend."

say "4. kubectl exec talks to the guest image (busybox httpd), not the host pause"
$K exec isolated-busybox1 -- ps
$K exec isolated-busybox1 -- cat /scratch/ok
$K exec isolated-busybox1 -- cat /tmp/marooned-ok
$K exec isolated-busybox1 -- ls /bin/httpd

say "5. Service reaches guest httpd (masquerade). status.podIP is the pause."
echo "  podIP    = $($K get pod isolated-busybox1 -o jsonpath='{.status.podIP}')"
echo "  guest-ip = $($K get pod isolated-busybox1 -o jsonpath='{.metadata.annotations.maroonedpods\.io/guest-ip}')"
$K get svc isolated-busybox1
$K run --rm -i --image=quay.io/prometheus/busybox:latest --restart=Never --image-pull-policy=Never demo-wget -- wget -qO- http://isolated-busybox1:8080/ || true

say "6. Killing the guest process is a real container exit"
$K exec isolated-busybox1 -- killall httpd || true
sleep 4
$K get pod isolated-busybox1

say "7. kubectl delete the Pod — VMI and virt-launcher go with it"
$K delete -f examples/sandbox-pod.yaml --wait=true
echo
$K get pods
$K get vmi
echo
echo "Nothing left to manage except the RuntimeClass."
echo "  Pod → CRI-O marooned-oci → marooned-shim → VMI + vsockfwd → guest agent"
echo "  Kata-shaped nginx:  kubectl apply -f examples/sandbox-nginx.yaml"
