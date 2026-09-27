# Sandbox follow-ups

Working branch: `sandbox-mode`. Isolation core works (VMI + user-rootfs +
logs + exec). Do not restore shim hostNetwork/hostPID or sidecar spc_t.

## Done (verified on kubevirtci)

- RuntimeClass=marooned Pod → hidden VMI; delete Pod deletes VMI
- vsock via virt-launcher sidecar (container_t, hostPath relabel)
- User image unpacked onto sized emptyDisk; chroot exec
- kubectl logs; kubectl exec (no -it)
- Mutating webhook does not re-add finalizer on deleting Pods

## In progress (this file)

### Group A — scheduling (Kata 1+2)  ← verified on kubevirtci
- Stop stripping hugepages / extended devices / resourceClaims from the user Pod
- Still strip PVC/ephemeral/emptyDir (cannot attach to user Pod and virt-launcher)
- Persist stripped volumes in annotation; translate a copy onto the VMI
- RuntimeClass podFixed = 100m + 256Mi (virt-launcher tax + guest kernel/agent)

### Group B — CRI stats (Kata 3)  ← verified (events --stats rss=430080 pids=1)
- Agent MethodStats from guest /proc (workload PID tree RSS + CPU ticks)
- Shim /v1/ContainerStats and /v1/PodSandboxStats
- marooned-oci events --stats (runc-shaped JSON)
- Query guest stats from the shim pod (hostPath /run/marooned-oci), not virt-handler
- Guest PID 1 is busybox sh (restarts agent). Go agent must not be init (exit 2 → kernel panic).
- log-pump HTTP client DisableKeepAlives so vsock sessions do not leak

### Group C — status (Kata 4)  ← verified (killall httpd → Error then Always restart)
- Workload runs in a guest PID namespace. Container PID 1 is a sh that forwards SIGTERM (kernel ignores SIGTERM to unhandled PID 1).
- When the guest process exits, marooned-oci waits, SIGTERM the host pause, and writes `/var/run/crio/exits/<id>` (conmon is not the pause parent, so waitpid never fires)
- PrepareRootfs is a no-op if the user-rootfs disk is already mounted (Always restart after killall)
- oci `state` is stopped if the guest process is dead (kernel panic no longer looks Running)
- ContainerStatus Ready/Pid/RestartCount/ExitCode from the agent
- podIP is still the CRI-O pause CNI address (kubelet owns it); workload IP remains maroonedpods.io/guest-ip
- QoS follows the unstripped user spec (Group A)
- kubelet may show "272y ago" on the Always restart timestamp (cosmetic)

### Group D — later
- exec -it: verified (os.Pipe + close parent ends; kubectl returns after the command)
- First-start: virt-launcher 3/3 in ~3s (IfNotPresent disks); guest exec works immediately. Warm pool still later if we need pre-booted VMs.
- Guest network: verified (10.0.2.2, Service wget, Ready 1/1 via exec probe pid-file). status.podIP is still the pause.
- Volumes: emptyDir tmpfs + filesystem PVC + block volumeDevices verified. RWX is virtio-blk too (no virtiofs; virt-launcher stays non-root).
- Image: ImageVolume FG for sandbox/kernel containerDisk. Guest-pull verified (`guest-pull quay.io/prometheus/busybox:latest ok`; no host tar). Host tar remains last-resort fallback.
- Multi-container / init: verified (`isolated-multi` Ready; `cat /shared/ready` → init-ok). One VMI; pause stays under conmon so init exit 0 is waitable.
- Large-image: Pod+image for the engine; weights on a data PVC. `examples/sandbox-model.yaml` verified (`/models/ok` and wget 127.0.0.1 → model-ok). No importer. `rootfs-volume` air-gap only.
- e2e: tests/e2e_sandbox_test.go already checks guest exec (`/tmp/index.html`, `/scratch/ok`, httpd) + logs, not only Running
- One cgroup: documented in docs/sandbox-mode.md (two cgroups; do not join qemu to the user Pod)
- Python: verified (`python3` 3.12.14, `/scratch/ok` → vol-ok, wget 127.0.0.1:8080 → marooned-python-ok)
- Kata-shaped nginx: verified (Alpine os-release, Welcome to nginx, Service ClusterIP wget from another sandbox pod)

## Still planned

GitHub issues track these. Next in order: #3 → #4 → #5 → #6.

- **kubectl top / HPA:** #3 — verified (`isolated-model` → 18Mi guest RSS, not pause). Shim annotation + `metrics.k8s.io`. CRI-O cgroup path still pause (node eviction unchanged). Idle CPU 0m is expected.
- **Guest `status.podIP`:** #4 — kubelet keeps pause CNI. Masquerade: pause forwards ports to virt-launcher. l2bridge/CUDN (production): EndpointSlice uses the guest IP; do not use l2bridge on kubevirtci.
- **Always-restart timestamp:** #5 — verified (`Finished: Sun, 27 Sep 2026`, not 1754/272y). Exit 143 is SIGTERM.
- **Dedicated CPUs:** #6 — Guaranteed QoS / annotation → VMI `dedicatedCPUPlacement`.
- **Rename this repo / product:** #7 — sandbox Pods are not marooned on a VM-node.
- **Warm pool:** #8
- **Devices (hugepages / SR-IOV / DRA):** #9
- **TEE:** #10
- **Live migration (later):** #11
- **e2e on cluster:** #12
- **Node-mode (other repo):** #13
- **containerd:** #14 — v1 is CRI-O (conmon, `/var/run/crio/exits`). Need a RuntimeClass path for containerd.
- **Image PVC importer:** not planned (engine image + weights PVC instead).
- **virtiofs / privileged virt-launcher:** not planned.

## Build/test contract

Agent writes code+tests, commits `--signoff`, then tells the human the
exact `cluster-push` / kubectl checks. No git push from the agent.
