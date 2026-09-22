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
- kubectl top / HPA still follow the pause cgroup until CRI-O is taught to call runtime events
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
- Guest network: load virtio_net in initrd (no eth0 → No route to host); probe timeout 10s (1s was CRI exit -1). Masquerade ports + EndpointSlice + exec probes. status.podIP is still the pause.
- Volumes into the guest (snapshot exists; agent mount table)
- Multi-container / init containers
- Large-image unpack (full tar over vsock)
- e2e asserts logs/exec/os-release, not only Running
- One cgroup: document guest+virt-launcher limits; do not join qemu to user pod cgroup yet
- Python (or similar) image smoke
- Node-mode (not in this repo)

## Build/test contract

Agent writes code+tests, commits `--signoff`, then tells the human the
exact `cluster-push` / kubectl checks. No git push from the agent.
