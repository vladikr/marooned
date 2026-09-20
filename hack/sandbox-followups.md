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

### Group A — scheduling (Kata 1+2)  ← code landed, needs cluster verify
- Stop stripping hugepages / extended devices / resourceClaims from the user Pod
- Still strip PVC/ephemeral/emptyDir (cannot attach to user Pod and virt-launcher)
- Persist stripped volumes in annotation; translate a copy onto the VMI
- RuntimeClass podFixed = 100m + 256Mi (virt-launcher tax + guest kernel/agent)

### Group B — CRI stats (Kata 3)
- Shim ContainerStats / PodSandboxStats from guest cgroup (+ optional launcher RSS)
- Until then kubectl top / HPA follow the pause container

### Group C — status (Kata 4)
- podIP = guest/l2bridge IP
- containerStatuses.ready = agent says process is up
- restartCount from the agent
- QoS from the unstripped spec

### Group D — later
- exec -it (code landed, needs cluster verify)
- First-start ~2min (VMI Scheduling + kubelet CRI timeout)
- Guest network vs Pod IP (Services/probes)
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
