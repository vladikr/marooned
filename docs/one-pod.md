# One-pod sandbox

Branch `one-pod` (this tree). Branch `two-pod` is the last VMI-adaptor
shape: user pause Pod + hidden `marooned-<pod-uid>` VMI.

The user API is unchanged: a Pod with `runtimeClassName: marooned`.
QEMU runs **in that Pod’s cgroup**. There is no second virt-launcher Pod
and no `VirtualMachineInstance` object.

## Why

Two Pods double-reserved guest RAM and let CPU manager exclusive-pin the
pause. Micro-VMs cannot afford that. Kubernetes has no display-only
resource field, so the live Pod spec is what kubelet honors.

## Shape

```
kubelet → CRI-O handler marooned → marooned-oci
                                      │
                                      ├─ pause PID = virt-launcher --standalone
                                      │    STANDALONE_VMI=<json>
                                      │    qemu in the user Pod cgroup
                                      └─ user containers: dummy + agent exec
```

- Translate still builds a VMI **spec** (disks, guest RAM, kernelBoot).
- That spec is passed to virt-launcher as `STANDALONE_VMI` (same as
  kubevirt-vm-to-pod / FlightCtl). It is not `Create()`d in the API.
- CPU manager / topology manager / requests apply to qemu.
- PVC/ephemeral stay on the user Pod. kubelet NodePublishes once.
  `volumeMounts.mountPath` in YAML is the **guest** path (annotation
  `maroonedpods.io/volumes`). Live bind-mount is
  `/run/marooned/disks/<volume>`.
- emptyDir stays guest tmpfs.

## CRI-O handler

`marooned-shim` writes `/etc/crio/crio.conf.d/20-marooned.conf` and
`marooned-oci`, probes `crio config` for `runtimes.marooned`, reloads,
and restarts CRI-O only if the handler is still missing.
`./hack/kubevirtci-install-crio-handler.sh` is force-repair (and guest
image refresh).

## Still to wire

- Install `virt-launcher` on the node (copy next to `marooned-oci`).
- Render `STANDALONE_VMI` at sandbox create (shim `Translate` + write
  `standalone-vmi.json` for marooned-oci).
- `/dev/kvm` in the sandbox.
- vsock without virt-handler.
- Guest-ip / port-forward from standalone networking (passt or
  masquerade).
- dedicatedCPUPlacement from original Guaranteed+integer CPU (issue #6)
  now lands on this same Pod.

`kubectl exec` / logs / probes stay on marooned-oci → agent.
