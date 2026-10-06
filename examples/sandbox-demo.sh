#!/usr/bin/env bash
# One-pod marooned sandbox walkthrough (qemu is the CRI pause PID).
#
#   export KUBEVIRT_PROVIDER=k8s-1.37
#   asciinema rec --overwrite --title "Marooned one-pod sandbox" \
#     --idle-time-limit 5 --window-size 120x36 --return \
#     --command "bash examples/sandbox-demo.sh" demo.cast
set -euo pipefail
cd "$(dirname "$0")/.."

KBIN="${K:-./cluster-up/kubectl.sh}"
PAUSE="${DEMO_PAUSE:-3}"
PREVIEW="${DEMO_PREVIEW:-2}"

k() {
	local n=0 rc=0
	local err out
	err=$(mktemp)
	out=$(mktemp)
	while :; do
		set +e
		"$KBIN" "$@" >"$out" 2>"$err"
		rc=$?
		set -e
		grep -vE 'selecting docker as container runtime|Emulate Docker CLI using podman' "$err" >&2 || true
		if [ "$rc" -eq 0 ]; then
			cat "$out"
			rm -f "$err" "$out"
			return 0
		fi
		if grep -qiE 'TLS handshake timeout|connection refused|connection reset|i/o timeout|EOF|temporary failure' "$err" && [ "$n" -lt 8 ]; then
			n=$((n + 1))
			sleep 4
			continue
		fi
		cat "$out"
		rm -f "$err" "$out"
		return "$rc"
	done
}

say() {
	printf '\n\n\033[1;36m## %s\033[0m\n' "$*"
	sleep "$PAUSE"
}

note() {
	printf '\033[0;37m%s\033[0m\n' "$*"
}

cmd() {
	printf '\n\033[1;33m$ %s\033[0m\n' "$*"
	sleep "$PREVIEW"
}

host_sh() {
	local node=$1
	local script=$2
	local shim
	shim=$(k -n marooned-system get pod -l maroonedpods.io=marooned-shim \
		--field-selector spec.nodeName="$node" -o jsonpath='{.items[0].metadata.name}')
	printf '%s\n' "$script" | k -n marooned-system exec -i -c shim "$shim" -- sh
}

# Match qemu/virt-launcher by the cgroup pod UID (underscores, not dashes).
show_pod_kvm() {
	local node=$1 uid=$2
	local uid_cg
	uid_cg=$(printf '%s' "$uid" | tr '-' '_')
	host_sh "$node" "
uid_cg='$uid_cg'
for d in /host-proc/[0-9]*; do
  c=\$(cat \$d/comm 2>/dev/null) || continue
  case \"\$c\" in virt-launch*|qemu-kvm) ;; *) continue ;; esac
  cg=\$(cat \$d/cgroup 2>/dev/null) || continue
  echo \"\$cg\" | grep -q \"\$uid_cg\" || continue
  echo
  echo \"process: \$c  pid=\${d##*/}\"
  tr '\\0' ' ' < \$d/cmdline | cut -c1-220
  echo
  echo \"cgroup: \$cg\"
  if [ \"\$c\" = qemu-kvm ]; then
    p=/host-proc/1/root/sys/fs/cgroup\$(echo \"\$cg\" | sed 's/^0:://')
    while [ -n \"\$p\" ] && [ \"\$p\" != /host-proc/1/root/sys/fs/cgroup ]; do
      if [ -f \$p/cpuset.cpus.effective ]; then
        echo -n 'cpuset.cpus.effective: '
        cat \$p/cpuset.cpus.effective
        break
      fi
      if [ -f \$p/cpuset.cpus ]; then
        v=\$(cat \$p/cpuset.cpus)
        if [ -n \"\$v\" ]; then
          echo \"cpuset.cpus: \$v\"
          break
        fi
      fi
      p=\$(dirname \$p)
    done
  fi
done
"
}

# DEMO_REUSE=1 skips delete/apply when the example pods are already Running.
REUSE="${DEMO_REUSE:-0}"

cleanup_demo_pods() {
	# Wait for gone: apply of a Terminating pod is an UPDATE and kubelet
	# rejects mixing dummy volumeMounts with YAML volumeDevices.
	k delete pod isolated-busybox1 isolated-pvc isolated-block isolated-multi demo-wget \
		--ignore-not-found --wait=true --timeout=90s >/dev/null 2>&1 || true
	k delete pvc isolated-data isolated-block-data \
		--ignore-not-found --wait=true --timeout=90s >/dev/null 2>&1 || true
	k delete svc isolated-busybox1 \
		--ignore-not-found --wait=true --timeout=30s >/dev/null 2>&1 || true
}

printf '\033[1mMarooned one-pod sandbox\033[0m\n'
note "User API: a Pod with runtimeClassName: marooned"
note "Isolation: virt-launcher/qemu is the CRI pause PID in that Pod cgroup"
note "There is no VMI object and no second virt-launcher Pod"
sleep "$PAUSE"

say "RuntimeClass marooned — handler plus overhead (virt-launcher tax + guest kernel)"
cmd "kubectl get runtimeclass marooned -o yaml"
k get runtimeclass marooned -o yaml
sleep "$PAUSE"

cmd "kubectl get pods -n marooned-system -o wide"
k get pods -n marooned-system -o wide
sleep "$PAUSE"

cmd "kubectl get pods -n maroonedpods"
k get pods -n maroonedpods
sleep "$PAUSE"

say "The Pod the user submits — ordinary containers, emptyDir, runtimeClassName"
cmd "cat examples/sandbox-pod.yaml"
sed -n '/^apiVersion: v1/,/^---$/p' examples/sandbox-pod.yaml | sed '$d'
sleep "$PAUSE"

if [ "$REUSE" = 1 ]; then
	note "Example pods are already Running."
else
	note "Resetting previous demo pods (keep isolated-cpu)."
	cleanup_demo_pods
	sleep 1

	cmd "kubectl apply -f examples/sandbox-pod.yaml"
	k apply -f examples/sandbox-pod.yaml
	sleep "$PAUSE"

	cmd "kubectl apply -f examples/sandbox-pod-pvc.yaml"
	k apply -f examples/sandbox-pod-pvc.yaml
	sleep "$PAUSE"

	cmd "kubectl apply -f examples/sandbox-pod-block.yaml"
	k apply -f examples/sandbox-pod-block.yaml
	sleep "$PAUSE"
fi

note "isolated-cpu is already Running (Guaranteed 1 CPU on the kubevirtci worker)."
cmd "kubectl get pods -o wide"
k get pods -o wide
sleep "$PAUSE"

say "Admission mutates the live spec; YAML paths stay in maroonedpods.io/volumes"
note "emptyDir is dropped (guest tmpfs). PVC stays. volumeDevices become dummy mounts."
note "Webhook adds Image volumes for the sandbox disk and kernel."
cmd "kubectl get pod isolated-busybox1 -o json | python3 -c '…'"
k get pod isolated-busybox1 -o json | python3 -c '
import json, sys
p = json.load(sys.stdin)
md, spec = p["metadata"], p["spec"]
ann = md.get("annotations") or {}
print("runtimeClassName:", spec.get("runtimeClassName"))
print("finalizers:", md.get("finalizers"))
print("placement:", ann.get("maroonedpods.io/placement"))
print("volumes:")
for v in spec.get("volumes") or []:
    kinds = [k for k in v if k not in ("name",)]
    print("  - %s: %s" % (v["name"], ",".join(kinds)))
c = spec["containers"][0]
print("volumeMounts:")
for m in c.get("volumeMounts") or []:
    print("  %s -> %s" % (m["name"], m["mountPath"]))
print("volumeDevices:", c.get("volumeDevices") or [])
print("image volumes (kubelet unpacks these for qemu):")
for v in spec.get("volumes") or []:
    if v.get("image"):
        print("  %s: %s" % (v["name"], v["image"].get("reference")))
snap = ann.get("maroonedpods.io/volumes")
print("volumes annotation (guest table):")
if snap:
    print(snap)
'
sleep "$PAUSE"

cmd "kubectl get pod isolated-block -o json | python3 -c '…'  # dummy mount, snapshot keeps /dev/xvda"
k get pod isolated-block -o json | python3 -c '
import json, sys
p = json.load(sys.stdin)
c = p["spec"]["containers"][0]
print("live volumeMounts:")
for m in c.get("volumeMounts") or []:
    print("  %s -> %s" % (m["name"], m["mountPath"]))
print("live volumeDevices:", c.get("volumeDevices") or [])
print("snapshot:", p["metadata"].get("annotations", {}).get("maroonedpods.io/volumes"))
'
sleep "$PAUSE"

say "Wait for Ready — nested kubevirtci DomainDefineXML is ~80-100s per VM"
cmd "kubectl wait --for=condition=Ready pod/isolated-busybox1 pod/isolated-pvc pod/isolated-block --timeout=240s"
k wait --for=condition=Ready pod/isolated-busybox1 pod/isolated-pvc pod/isolated-block --timeout=240s
sleep "$PAUSE"

cmd "kubectl get pods -o wide"
k get pods -o wide
sleep "$PAUSE"

say "No VMI object, no virt-launcher Pod — qemu lives in the user Pod"
cmd "kubectl get vmi -A"
k get vmi -A || true
sleep "$PAUSE"

cmd "kubectl get pods -l kubevirt.io=virt-launcher -A"
k get pods -l kubevirt.io=virt-launcher -A
sleep "$PAUSE"

NODE=$(k get pod isolated-busybox1 -o jsonpath='{.spec.nodeName}')
PODUID=$(k get pod isolated-busybox1 -o jsonpath='{.metadata.uid}')
note "isolated-busybox1 is on ${NODE}, uid=${PODUID}"

say "CRI-O handler marooned — marooned-oci + conmon, pause PID is virt-launcher"
cmd "cat /etc/crio/crio.conf.d/20-marooned.conf   # via shim host-proc on ${NODE}"
host_sh "$NODE" 'cat /host-proc/1/root/etc/crio/crio.conf.d/20-marooned.conf'
sleep "$PAUSE"

cmd "ps/cgroup for virt-launcher and qemu belonging to this Pod"
show_pod_kvm "$NODE" "$PODUID"
sleep "$PAUSE"

say "Guest workload: logs, exec, emptyDir tmpfs, podIP"
cmd "kubectl logs isolated-busybox1"
k logs isolated-busybox1 | tail -20
sleep "$PAUSE"

cmd "kubectl exec isolated-busybox1 -- ps"
k exec isolated-busybox1 -- ps
sleep "$PAUSE"

cmd "kubectl exec isolated-busybox1 -- cat /scratch/ok"
k exec isolated-busybox1 -- cat /scratch/ok
echo
sleep "$PAUSE"

cmd "kubectl exec isolated-busybox1 -- wget -qO- http://127.0.0.1:8080/"
k exec isolated-busybox1 -- wget -qO- http://127.0.0.1:8080/
echo
sleep "$PAUSE"

PODIP=$(k get pod isolated-busybox1 -o jsonpath='{.status.podIP}')
cmd "kubectl get pod isolated-busybox1 -o jsonpath='{.status.podIP}'"
echo "podIP = ${PODIP}"
note "Guest NIC is the pod-network bridge, so curl of status.podIP:8080 reaches httpd in the VM."
sleep "$PAUSE"

cmd "kubectl run demo-wget --rm -i --restart=Never --image=quay.io/prometheus/busybox:latest -- wget -qO- http://${PODIP}:8080/"
k run demo-wget --rm -i --restart=Never --image=quay.io/prometheus/busybox:latest --image-pull-policy=Never -- wget -qO- "http://${PODIP}:8080/" || true
echo
sleep "$PAUSE"

say "Filesystem PVC — agent mkfs/mounts virtio-blk at /data"
cmd "kubectl exec isolated-pvc -- cat /data/ok"
k exec isolated-pvc -- cat /data/ok
echo
sleep "$PAUSE"

say "Block volumeDevices — raw virtio-blk at /dev/xvda, no mkfs"
cmd "kubectl exec isolated-block -- dd if=/dev/xvda bs=8 count=1"
k exec isolated-block -- dd if=/dev/xvda bs=8 count=1
echo
sleep "$PAUSE"

say "Init + app share a guest tmpfs (one VM, kubelet owns the order)"
cmd "sed -n '1,28p' examples/sandbox-pod-multi.yaml"
sed -n '1,28p' examples/sandbox-pod-multi.yaml
note "Init writes /shared/ready; the app serves it. Nested virt can miss kubelet's init timeout, so this recording skips a live apply."
sleep "$PAUSE"

say "Dedicated CPUs — Guaranteed integer CPU, qemu already in the Pod cgroup"
cmd "kubectl get pod isolated-cpu -o wide"
k get pod isolated-cpu -o wide
k get pod isolated-cpu -o jsonpath='qos={.status.qosClass} cpu={.spec.containers[0].resources.requests.cpu}/{.spec.containers[0].resources.limits.cpu} mem={.spec.containers[0].resources.requests.memory} node={.spec.nodeName}{"\n"}'
sleep "$PAUSE"

CPU_NODE=$(k get pod isolated-cpu -o jsonpath='{.spec.nodeName}')
CPU_UID=$(k get pod isolated-cpu -o jsonpath='{.metadata.uid}')
cmd "qemu / cpuset for isolated-cpu on ${CPU_NODE}"
show_pod_kvm "$CPU_NODE" "$CPU_UID"
sleep "$PAUSE"

say "kubectl top — guest RSS via maroonedpods.io/guest-stats, not the pause cgroup"
cmd "kubectl apply -f examples/sandbox-metrics-apiservice.yaml"
k apply -f examples/sandbox-metrics-apiservice.yaml
sleep 2
k wait --for=condition=Available apiservice/v1beta1.metrics.k8s.io --timeout=60s || true
sleep "$PAUSE"

cmd "kubectl top pod"
k top pod || true
note "top MEMORY is guest RSS rounded to Mi. Idle busybox is under 1Mi, so 0Mi is expected."
sleep "$PAUSE"

cmd "kubectl get pod isolated-busybox1 -o jsonpath='{.metadata.annotations.maroonedpods.io/guest-stats}'"
k get pod isolated-busybox1 -o jsonpath='{.metadata.annotations.maroonedpods\.io/guest-stats}' | python3 -c 'import json,sys; s=json.load(sys.stdin); print(json.dumps(s)); print("guest RSS %.0f KiB  pids %s  milliCPU %s" % (s["rssBytes"]/1024.0, s["pids"], s.get("milliCPU")))'
sleep "$PAUSE"

say "Overhead — one cgroup, one reservation"
note "Two-pod reserved guest RAM twice (user pause + virt-launcher) and could pin the pause."
note "One-pod: qemu is the pause. RuntimeClass podFixed is 100m + 256Mi (launcher tax + kernel)."
note "Guest RAM is the Pod memory request. ExtraGuestOverhead 50m does not ceil 1 CPU to 2 cores."
cmd "kubectl get pod isolated-busybox1 isolated-cpu -o custom-columns=NAME:.metadata.name,QOS:.status.qosClass,REQ_CPU:.spec.containers[0].resources.requests.cpu,REQ_MEM:.spec.containers[0].resources.requests.memory,OVERHEAD:.spec.overhead"
k get pod isolated-busybox1 isolated-cpu -o custom-columns='NAME:.metadata.name,QOS:.status.qosClass,REQ_CPU:.spec.containers[0].resources.requests.cpu,REQ_MEM:.spec.containers[0].resources.requests.memory,OVERHEAD:.spec.overhead'
sleep "$PAUSE"

say "What this cluster is vs production"
note "kubevirtci: kernel pod-network bridge; guest IP equals status.podIP."
note "Production: l2bridge / primary UDN (CUDN). Do not enable l2bridge here."
note "Runtime is CRI-O (marooned-oci + conmon). containerd is not wired yet."
note "Nested DomainDefineXML is slow; a real node is much closer to virt-launcher start."
sleep "$PAUSE"

printf '\n\033[1;32mDone.\033[0m kubectl logs / exec / top / delete all target the user Pod.\n'
sleep 2
