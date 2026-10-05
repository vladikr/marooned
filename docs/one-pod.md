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
                                      ├─ pause PID = virt-launcher
                                      │    cmd SyncVirtualMachine
                                      │      domainAttachments[default]=tap
                                      │    qemu joined to the OCI cgroupsPath
                                      └─ user containers: dummy + agent exec
```

- Translate still builds a VMI **spec** (disks, guest RAM, kernelBoot).
- That spec is sent to virt-launcher over cmd `SyncVirtualMachine`
  (same JSON as `STANDALONE_VMI`, plus `VirtualMachineOptions`).
  It is not `Create()`d in the API.
- CPU manager / topology manager / requests apply to qemu.
  `marooned-oci` joins virt-launcher and qemu to the pause container’s
  `linux.cgroupsPath` (never the root cgroup). libvirt
  `cgroup_controllers` is cleared so virtqemud does not move qemu into
  `machine.slice`.
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

## STANDALONE_VMI and VEP-359

KubeVirt VEP-359 is merged (kubevirt/enhancements#360). The in-tree
implementation is still open: kubevirt/kubevirt#19233 (ClusterConfig
interfaces) and kubevirt/kubevirt#19234 (`pkg/render` `PodFromVMI`).
marooned does **not** import `kubevirt.io/kubevirt/pkg/render` until
those land. The VEP leaves `STANDALONE_VMI` and other edge post-processing
in the consuming tool.

Today:

1. Shim `POST /v1/RenderStandaloneVMI` runs `pkg/sandbox/standalone.Render`
   (`translate.Translate` → JSON, Pod UID stamped on the VMI).
2. `marooned-oci create` writes `standalone-vmi.json` (and
   `/run/marooned-oci/vmi-<pod-uid>.json` so a container restart still
   has the spec) and starts
   virt-launcher **without** `STANDALONE_VMI`. v1.9
   `HandleStandaloneMode` would `SyncVMI` with nil
   `VirtualMachineOptions`, so the converter leaves
   `<interface type="">`. After `markReady` (launcher-sock),
   marooned-oci calls cmd `SyncVirtualMachine` with
   `interfaceDomainAttachment[default]=tap`.
3. The shim init container copies the **virt-launcher image rootfs** to
   `/opt/marooned/virt-launcher-rootfs` (qemu+libvirt live there; a
   host binary copy is not enough). Each sandbox gets an overlay on
   that rootfs, then `nsenter --net=<pod netns> chroot … virt-launcher`.
4. `/dev/kvm`, `/dev/net/tun`, `/dev/vhost-net`, and `/dev/vhost-vsock`
   are bind-mounted into that overlay.
5. The mutating webhook adds k8s Image volumes for the sandbox disk and
   kernel images. kubelet unpacks them under
   `/var/lib/kubelet/pods/<uid>/volumes/kubernetes.io~image/`. marooned-oci
   bind-mounts those roots into the overlay at virt-launcher ImageVolume
   paths (`/var/run/kubevirt-image-volume/disk_0`,
   `/var/run/kubevirt-kernel-boot`) and passes `--image-volume`.
   kubevirtci CRI-O plants Image volumes as OCI overlay mounts (lowerdir
   of the pulled image), not under `kubernetes.io~image`; marooned-oci
   reads those from the bundle. `containerDisk.path` stays empty:
   virt-launcher v1.9 looks under `disk_0/disk/` (scratch
   `COPY disk.qcow2 /disk/disk.qcow2`).
6. `VMI.Status.VSOCKCID` is hashed from the Pod UID. marooned-oci writes
   the cid file and runs `vsockfwd` in the pod netns so the shim can dial
   `unix:/var/run/marooned/<uid>/agent.sock` with no VMI object.
7. The STANDALONE_VMI pod NIC is `bridge: {}` (not masquerade, not
   Passt). marooned-oci runs virt-handler phase 1 in the user Pod netns:
   rename `eth0` → `eth0-nic` (new local MAC so the bridge FDB does
   not pin the guest MAC on the veth), bridge `k6t-eth0`, tap `tap0`,
   dummy `eth0` keeps the original MAC/IP for CNI CHECK, DHCP cache at
   `/var/run/kubevirt-private/vif-cache-eth0.json`. Then
   `SyncVirtualMachine` with `domainAttachments[default]=tap` so the
   converter emits `<interface type="ethernet">` and virt-launcher
   phase 2 owns tap0 and DHCP. If phase 1 fails, the NIC is stripped
   so the guest still boots on vsock.
   Machine type is i440fx (`pc`) with `DisableHotplug`: virt-launcher on
   q35 defines the domain twice to reserve pcie-root-ports, and the
   second `DomainDefineXML` omits `<uuid>` so libvirt rejects the name.
8. User image fill: CRI-O already unpacked the tree on the host
   (`/var/lib/containers/storage/overlay/.../merged`). The shim cannot
   `stat` that path (no hostPID). It tars via
   `nsenter --mount=/host-proc/1/ns/mnt`. Guest-pull can use the NIC
   once DHCP lands.

When `PodFromVMI` is importable, swap the JSON source (and later the
containerDisk layout). Keep RuntimeClass + marooned-oci as the user Pod API.

## Still to wire

- Guest-ip / Services on kubevirtci: kernel bridge is in (this tree).
  Confirm `kubectl exec` wget of `status.podIP:8080` from another pod.
  Production stays l2bridge/CUDN; do not enable l2bridge on kubevirtci.
- PVC hostPath bind into the overlay (`/var/lib/kubelet/pods/<uid>/volumes`).
- dedicatedCPUPlacement from original Guaranteed+integer CPU (issue #6)
  now lands on this same Pod.

`kubectl exec` / logs / probes stay on marooned-oci → agent.
