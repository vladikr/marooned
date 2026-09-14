# MaroonedPods sandbox mode — Grok build instructions

Implementation repo is now https://github.com/vladikr/marooned (sandbox only).
Node mode remains at https://github.com/vladikr/maroonedpods.

Implement this. Do not redesign it. Do not revive the k3s-node path.

Repo: https://github.com/vladikr/marooned
Module: `maroonedpods.io/maroonedpods`
Language: Go, same operator layout as today
(`cmd/`, `pkg/maroonedpods-controller/`, `pkg/maroonedpods-operator/`,
`pkg/maroonedpods-server/`, `staging/src/maroonedpods.io/api/`).

One existing KubeVirt CR in the cluster. Never install a second KubeVirt/HCO.

---

## 1. Goal

Users write a **Pod** with `runtimeClassName: marooned`.
The container runs in a KubeVirt guest (kernel + agent, no guest kubelet).
`kubectl logs` / `exec` / probes / Services work on that Pod.
QEMU, CSI, SR-IOV, DRA, hugepages stay on a **hidden virt-launcher Pod**
because that is the KubeVirt-native path.

Same job as Kata. Overhead target is “no guest node”; libvirt/virt-launcher
stripping is a later KubeVirt change, not this repo’s first milestone.

Confidential compute is the same object model with
`launchSecurity` on the hidden VMI and attestation in the guest
(Phase 6). It is not a second RuntimeClass and not a second KubeVirt.

## 2. Non-goals (do not implement)

- Guest k3s / kubelet / “VM is a Node” for this mode
- User-visible VMI in the application namespace
- virtiofs as container rootfs
- Running virt-launcher as root to make virtiofs RW
- Second KubeVirt CR or second virt-controller
- Merging virt-controller’s generated pod spec back onto the user Pod
- Per-container RuntimeClass (it does not exist)
- Multi-container pods beyond one pause + one workload in v1
- Live migration of the hidden VMI in v1 (design so it is not painted into a corner)
- SNP/TDX + SR-IOV/PCI passthrough in the same VMI (KubeVirt CC PoC does not support it)
- In-cluster evidence verification by KubeVirt or by maroonedpods on the hypervisor node
- First-class SNP hostData / TDX mrConfigId (InitData is not in tree; expect zeros)

## 3. Object model

| Object | Namespace | Visible to app user | Role |
|---|---|---|---|
| Pod | user ns | yes | CRI identity, logs, exec, probes, Service endpoints |
| RuntimeClass `marooned` | cluster | yes | handler name + podFixed overhead |
| VMI | `marooned-system` | no | sandbox VM |
| virt-launcher Pod | `marooned-system` | no | qemu, CSI attach, VFIO, hugepages, l2bridge |
| MaroonedPodsConfig | cluster | admin | images, pool size, bindings |

Labels on hidden VMI:

```
maroonedpods.io/mode=sandbox
maroonedpods.io/pool-state=creating|available|claimed
maroonedpods.io/claimed-by=<namespace>/<pod>
maroonedpods.io/sandbox-id=<cri-sandbox-id>
maroonedpods.io/node=<worker-name>
maroonedpods.io/tee=off|snp|tdx
```

OwnerRef: VMI → user Pod (so delete is GC). Pool VMIs have no ownerRef
until claimed.

## 4. Components to build

```
cmd/marooned-shim/          # node DaemonSet; containerd/CRI-O runtime handler
cmd/marooned-agent/         # guest process; vsock server
cmd/maroonedpods-controller # extend existing binary
images/sandbox/             # kernel + initrd + agent rootfs (NOT images/node)
pkg/sandbox/
  adaptor/                  # Pod → VMI claim/create/translate
  pool/                     # warm pool of sandbox VMIs
  translate/                # resources, volumes, devices, network
  vsock/                    # dial agent via virt-handler / virtctl-equivalent
pkg/webhook/                # mutate RuntimeClass pods
pkg/cri/                    # shim server (can live next to cmd)
```

Keep `pkg/maroonedpods-controller/maroonedpods-gate-controller/` for
`mode: Node` only. Do not route RuntimeClass pods through the gate.

## 5. Config API

Extend `MaroonedPodsConfig` (`maroonedpods.io/v1alpha1`). Keep backward
compatible defaults for node mode.

```yaml
apiVersion: maroonedpods.io/v1alpha1
kind: MaroonedPodsConfig
metadata:
  name: default
spec:
  defaultMode: Sandbox          # Sandbox | Node
  sandbox:
    infraNamespace: marooned-system
    kernelBoot:
      image: quay.io/vladikr/marooned-sandbox-kernel:latest
      kernelPath: /boot/vmlinuz
      initrdPath: /boot/initrd
      kernelArgs: "console=hvc0 marooned.agent=vsock"
    rootfsImage: quay.io/vladikr/marooned-sandbox:latest
    agentListen: vsock           # vsock | tcp (dev only)
    warmPoolSize: 2
    poolSizeClasses:
      - name: s
        guestCPU: "1"
        guestMemory: 512Mi
      - name: m
        guestCPU: "2"
        guestMemory: 2Gi
    network:
      binding: l2bridge          # l2bridge | masquerade (dev fallback)
      # pod: {} is always the network source
    publishGuestIPOnPod: true
    extraGuestOverhead:
      cpu: 50m
      memory: 64Mi
    confidentialCompute:
      # off | snp | tdx | annotation
      # annotation: Pod annotation maroonedpods.io/tee=snp|tdx
      default: off
      requireCapableNode: true
      trustee:
        enabled: false
        kbsURL: ""
```

Do not require a new CRD for v1 unless translation state cannot fit on
VMI annotations. Prefer annotations + labels on the VMI.

## 6. RuntimeClass

Install:

```yaml
apiVersion: node.k8s.io/v1
kind: RuntimeClass
metadata:
  name: marooned
handler: marooned
overhead:
  podFixed:
    cpu: 100m
    memory: 128Mi
```

`handler: marooned` must match containerd/CRI-O config on every worker
that should run these pods:

```
# containerd example
[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.marooned]
  runtime_type = "io.containerd.marooned.v1"
  # or a shim v2 binary path; pick one and document it
```

v1 may implement the shim as a **gRPC CRI runtime service** that
containerd is configured to use. Do not invent a second kubelet.

## 7. Webhook (user Pod)

On create/update of a Pod with `spec.runtimeClassName: marooned`:

1. Add finalizer `maroonedpods.io/sandbox-cleanup`.
2. Strip from the **user** Pod (move to VMI via adaptor, not drop):
   - `resources` hugepages-*
   - extended resources (`intel.com/*`, GPUs, etc.)
   - `resourceClaims`
   - `volumeMounts` / `volumes` that are PVC / ephemeral / emptyDir
     Keep a projected SA token if kubelet must mount one for the shim;
     do not keep workload PVCs on the user Pod.
3. Leave cpu/memory **requests** on the user Pod for scheduler fairness
   *or* copy them only to the VMI and leave a small shim request.
   Preferred v1: user Pod keeps cpu/memory requests; devices/hugepages/PVCs
   go only to the VMI. Document the double-count of cpu/memory
   (user Pod + virt-launcher). Follow-up: strip cpu/memory from user Pod
   and pin via nodeName after VMI bind (more correct, more code).
4. Do **not** add a scheduling gate for sandbox mode.
5. Do **not** add node taints/tolerations from the old node mode.

If `runtimeClassName` is empty and label `maroonedpods.io/maroon=true`,
leave the old gate path alone.

## 8. Adaptor (cluster controller)

Watch: Pods with RuntimeClass marooned, VMIs in `infraNamespace`.

### 8.1 Create / claim

When a sandbox Pod is Pending/creating:

1. Resolve target node:
   - If Pod is already bound (`spec.nodeName`), use that node.
   - Else let kubelet bind the user Pod first (normal scheduler), then
     claim a VMI **on that same node**. Same-node is mandatory
     (vsock, VFIO, CSI).
2. Pick size class from Pod cpu/memory requests + `extraGuestOverhead`.
3. Claim an `available` pool VMI on that node with matching or larger
   size class. Label `claimed-by`, set ownerRef to the Pod.
4. If none: create a VMI on that node (cold path).
5. Apply translation (section 10) as a VMI update (hotplug later;
   v1 may recreate if the pool VM lacks devices).
6. Wait until:
   - VMI Running
   - agent reachable on vsock
   - requested disks/VFs reported attached
7. Annotate the user Pod:
   `maroonedpods.io/vmi=marooned-system/<name>`
   `maroonedpods.io/guest-ip=<cudn-ip>`
8. If `publishGuestIPOnPod: true`, patch Pod status.podIP / podIPs
   to the guest CUDN address **only if** you can do it without
   fighting kubelet. If kubelet overwrites status, publish via
   an endpoint slice owned by a companion Service or document
   that Services must target a label and the adaptor writes
   Endpoints. Pick one and implement it. Preferred: adaptor-owned
   Endpoints/EndpointSlice for Services; do not fight kubelet
   on status.podIP in v1 if it flaps.

### 8.2 Delete

On Pod deletion (finalizer):

1. Tell shim/agent to stop containers (best effort).
2. If pool has room: detach extra disks/VFs, scrub, set
   `pool-state=available`, clear ownerRef and claimed-by.
3. Else delete VMI.
4. Remove finalizer.

Never delete a virt-launcher by hand. Delete the VMI.

## 9. VMI template (sandbox)

Always:

- Namespace: `spec.sandbox.infraNamespace`
- `autoattachGraphicsDevice: false`
- no cloud-init unless required for agent config
- no guest-console-log sidecar unless debugging
- Boot method is **mode-dependent**:
  - default (no TEE): `firmware.kernelBoot` from config
  - SNP/TDX: **UEFI**, `secureBoot: false`, no kernelBoot.
    TDX also `features.smm.enabled: false`.
    Measurement includes OVMF; kernelBoot is the wrong launch path
    for this PoC.
- containerDisk/rootfs volume = `rootfsImage` (minimal agent OS).
  TEE image must expose `/dev/sev-guest` or `/dev/tdx_guest`.
- interface:

```yaml
interfaces:
- name: default
  binding:
    name: l2bridge
networks:
- name: default
  pod: {}
```

Fallback binding `masquerade` only when config says so (dev clusters
without primary UDN).

- resources: guest memory = pod memory request + extraGuestOverhead
- DedicatedCPU / NUMA: off unless the Pod asked
- nodeSelector: `kubernetes.io/hostname: <bound-node>`
  plus `kubevirt.io/sev=true` or `kubevirt.io/tdx=true` when TEE is on
- cpu.model: `host-passthrough` when TEE is on
- launchSecurity: `snp: {}` or `tdx: {}` when TEE is on
- TDX VMs consume `devices.kubevirt.io/tdx` via virt-launcher (KubeVirt
  device plugin). Do not also put that resource on the user Pod.

Agent must start without a guest kubelet. PID 1 can be the agent or a
tiny init that execs the agent.

## 10. Translation (Pod → VMI)

Implement in `pkg/sandbox/translate`. Table-driven tests required.

### CPU / memory

```
sum(container requests.cpu)    + overhead.cpu    → guest vCPU (ceil to 1)
sum(container requests.memory) + overhead.memory → domain.memory.guest
```

### Hugepages

```
requests.hugepages-2Mi / 1Gi  → domain.memory.hugepages.pageSize
                                 + virt-launcher resource requests
```

Do not leave hugepages on the user Pod.

### SR-IOV / host devices

```
resources.intel.com/<resource> → devices.interfaces[].sriov
                                 + networks[].multus.networkName
```

NAD name: convention `maroonedpods.io/sriov-network` annotation on the
Pod if present, else a config default. Fail the Pod (Event + status)
if a VF is requested and no NAD is known.

If the Pod also requested a TEE (`maroonedpods.io/tee=snp|tdx` or
config default), **fail**. The KubeVirt CC PoC does not support PCI
passthrough together with launchSecurity. Hugepages + TEE is allowed.

### DRA

```
spec.resourceClaims → VMI domain.devices.gpus / hostDevices
                      using KubeVirt DRA fields (GPUsWithDRA etc.)
```

If the cluster KubeVirt lacks the gate, Event and fail. Do not invent
a side channel.

### Storage (native / efficient path)

| Pod volume | VMI | Guest agent |
|---|---|---|
| PVC Block | virtio-blk disk | bind device or mount if formatted |
| PVC Filesystem RWO | virtio-blk disk (not virtiofs) | mount filesystem, bind to volumeMount.path |
| PVC Filesystem RWX | virtiofs **only this case** | mount -t virtiofs |
| emptyDir | virtio empty disk **or** guest tmpfs | mount at path |
| configMap/secret/projected | extra small disk (iso/fs) or agent files | write into the container root |
| container image | **not a disk of layers via virtiofs** | see images |

Image start policy for v1 (pick **ImageVolume / containerDisk** first;
it is KubeVirt-native and needs no virtiofs):

1. Adaptor attaches the workload image as a KubeVirt ImageVolume or
   containerDisk if the image can be used that way.
2. Else agent pulls the image **inside the guest** (needs guest network
   on CUDN and a pull secret projected as a disk).
3. Never unpack on the host and virtiofs the directory in v1.

CSI attach happens only on virt-launcher. Webhook already stripped
PVCs from the user Pod so kubelet does not NodePublish them twice.

## 11. CRI shim (node)

Binary: `cmd/marooned-shim`.

DaemonSet on workers, privileged enough to:

- talk to containerd/CRI-O as the `marooned` handler
- dial the guest agent (vsock via virt-handler socket or a localhost
  proxy the adaptor exposes per sandbox)

Must implement the CRI subset kubelet actually calls for a single
container pod:

- RunPodSandbox / StopPodSandbox / RemovePodSandbox
- CreateContainer / StartContainer / StopContainer / RemoveContainer
- ContainerStatus / PodSandboxStatus
- ExecSync / Exec / Attach (streaming)
- PortForward (optional v1; Services via guest IP are enough)
- List* (return what you created)

Behavior:

- `RunPodSandbox` blocks until adaptor annotated the Pod with a VMI
  and agent is reachable. Timeout ~60s cold, ~5s warm. Error back to
  kubelet on timeout.
- `CreateContainer`/`StartContainer` = gRPC to agent.
- Logs: agent streams stdout/stderr; shim presents them as the
  container log file kubelet expects **or** implements the CRI
  `ReopenContainerLog` path. Must work with `kubectl logs`.
- Do not start runc containers for the workload. A pause process on
  the host is acceptable if CRI requires a local sandbox pid.

Authz: shim talks to apiserver with its SA to read Pod annotations
(`maroonedpods.io/vmi`). Do not watch all VMIs cluster-wide from every
node if you can avoid it.

## 12. Guest agent

Binary: `cmd/marooned-agent`, baked into `images/sandbox`.

v1 protocol: simple gRPC over vsock (do not take a Kata agent
dependency unless it is faster to vendor). Messages:

- Start(container spec: image, cmd, env, mounts)
- Stop / Wait
- Exec
- Logs (stream)
- MountTable (volumeMounts already attached as virtio disks)

The agent:

- Does not run containerd if you can exec the process from a
  prepared root (ImageVolume). If you need image pull, embed a
  tiny pull+unpack or run containerd-in-guest as a follow-up.
- Mounts virtio disks at the paths from the spec.
- Puts the workload in a mount/pid/net/uts/ipc namespace **inside
  the guest**. Guest netns should keep the l2bridge NIC so CUDN IP
  stays on the workload (or NAT inside guest — prefer keep NIC in
  the container netns, Kata-style).

Health: vsock ping from shim.

## 13. Sandbox image

`images/sandbox/`, separate from `images/node/`.

Two flavors, same agent:

- `images/sandbox/` — kernel + initrd for kernelBoot (non-TEE, density)
- `images/sandbox-tee/` — UEFI-bootable containerDisk, guest kernel with
  `CONFIG_SEV_GUEST` / TDX guest, `/dev/sev-guest` or `/dev/tdx_guest`,
  agent + optional `snpguest` / `trustee-attester`. Secure Boot off.

Neither image contains kubelet, k3s, or a node CNI.
Non-TEE target size: tens of MB. TEE image will be larger (OVMF + distro
userspace); still not a node image.

Build via existing Makefile style (`make build-sandbox-image`).

## 14. Warm pool

Reuse the idea in the current pool work. New labels, new image.

- Reconcile every 30s: per-node `available + creating >= warmPoolSize`
  (or cluster-wide if per-node is too much for v1; **prefer per-node**
  because claim is node-local).
- Pool VMIs use the smallest size class. On claim, if the Pod needs
  a bigger class, do not use that pool VM; create cold or keep
  per-class pools (v1: per-class per-node is fine if warmPoolSize is
  small).
- Claiming is atomic (patch with resourceVersion).
- Pool key is `(node, sizeClass, tee)`. A SNP VMI is not an available
  TDX VMI and is not an available non-TEE VMI. Do not cross-claim.

## 15. Networking

- Production: primary UDN/CUDN namespace + `binding.name: l2bridge`
  + `networks: [{ name: default, pod: {} }]`.
- `marooned-system` must be selected by the CUDN/UDN the app ns uses,
  **or** the VMI must be created in the **user namespace** but
  unlabeled/hidden (RBAC + UI). Prefer infra ns + CUDN that selects
  both `app` and `marooned-system`. Document the CUDN requirement.
- Dev fallback: masquerade. Guest IP ≠ Service IP; still ship it
  so unit tests run without OVN-K.
- SR-IOV is a **second** interface, never a replacement for l2bridge.

## 16. Confidential computing (SNP / TDX)

This is the KubeVirt CC PoC applied to the **hidden sandbox VMI**.
The user still writes a Pod. Attestation happens **inside the guest**
and is verified **off the hypervisor**. Maroonedpods does not become
a verifier.

Source of truth for host/guest knobs:
`PoC: SEV-SNP and TDX confidential VMs with attestation on KubeVirt`.

### 16.1 Cluster prerequisites (do not install a second KubeVirt)

On the **existing** KubeVirt/HCO CR only:

```yaml
spec:
  configuration:
    developerConfiguration:
      featureGates:
        - WorkloadEncryptionSEV
        - WorkloadEncryptionTDX
    confidentialCompute:
      tdx:
        attestation:
          enforced: true
          qgsSocketPath: /var/run/tdx-qgs/qgs.socket
```

OpenShift: HCO jsonpatch annotation, same fields. Confirm they survived
reconcile. Never `kubectl edit` the KubeVirt CR on OCP.

Node checks (labeller / device plugin already in KubeVirt 1.8+):

- SNP: `kubevirt.io/sev=true`, `/dev/sev`, `kvm_amd.sev_snp=1`
- TDX: `kubevirt.io/tdx=true` and/or `devices.kubevirt.io/tdx`,
  QGS socket `/var/run/tdx-qgs/qgs.socket` with perms qemu (uid 107)
  can connect (`-m=0666` or a shared group). PCCS reachable.

Adaptor does not install QGS/PCCS. If TDX is requested and the node
has no socket, the VMI stays Pending; surface that as a Pod Event.

### 16.2 How the user asks for a TEE

Annotation on the Pod (RuntimeClass stays `marooned`):

```yaml
metadata:
  annotations:
    maroonedpods.io/tee: snp    # or tdx
spec:
  runtimeClassName: marooned
```

Or cluster default `sandbox.confidentialCompute.default: snp|tdx`.

Webhook does not strip this annotation. Adaptor reads it when
building the VMI.

### 16.3 VMI fields the adaptor must set

SNP:

```yaml
domain:
  firmware:
    bootloader:
      efi:
        secureBoot: false
  cpu:
    model: host-passthrough
  launchSecurity:
    snp: {}
  devices:
    rng: {}
# nodeSelector:
#   kubevirt.io/sev: "true"
#   kubernetes.io/hostname: <bound-node>
```

TDX: same plus `features.smm.enabled: false`, `launchSecurity.tdx: {}`,
`nodeSelector.kubevirt.io/tdx: "true"`. No kernelBoot. No Secure Boot.

Do not set both `sev` and `snp`. Do not enable SMM on TDX.

`host_data` (SNP) and `mrConfigId` (TDX) stay zero until KubeVirt
InitData exists. Identity for policy is **measurement / MRTD + pinned
sandbox-tee image + OVMF**, not a KubeVirt-injected blob.

### 16.4 Guest attestation (agent + tools)

Sandbox-tee image includes:

- TEE device nodes working (`/dev/sev-guest` or `/dev/tdx_guest`)
- `snpguest` (SNP) and/or TDX quote helper
- optional `trustee-attester` if `trustee.enabled`

Agent RPCs to add in Phase 6 (not Phase 2):

- `Attest(nonce) -> evidence bytes`
- optional `ReleaseSecret(kbsURL)` when Trustee is configured

Shim / adaptor must **never** verify evidence on the worker that
runs the VMI. Verification is:

- CI/dev: copy evidence off and `snpguest verify` / Intel PCS on
  another machine
- prod-shaped: guest attester → Trustee KBS/AS on a different node
  or cluster

Trustee policy for this PoC:

- TEE type snp or tdx
- debug bit off
- measurement/MRTD equals the golden value recorded from a known
  sandbox-tee boot
- host_data / mr_config_id ignored or required-zero

Encrypted root (optional extra, guest-image work only): LUKS root,
initramfs attests, KBS returns passphrase. VMI YAML does not change.

### 16.5 Conflicts the translator must enforce

| Combo | v1 behavior |
|---|---|
| TEE + kernelBoot | reject; use UEFI sandbox-tee image |
| TEE + SR-IOV / GPU / hostDevices | reject; PoC has no PCI passthrough |
| TEE + virtiofs RWX | reject; host-visible tree fights the threat model |
| TEE + live migration | reject until KubeVirt supports it |
| TEE + guest-pull from host-untrusted registry | allowed; prefer this over host ImageVolume if the threat model includes a malicious host seeing layers |
| TEE + hugepages | allow |
| TEE + l2bridge/CUDN | allow |

Workload image handling under TEE: **guest-pull inside the CVM** is
the CoCo-aligned path. Host-attached ImageVolume exposes layers to
the virt-launcher node. Phase 6 default for TEE pods: guest-pull.
Non-TEE pods keep ImageVolume/containerDisk.

### 16.6 Overhead

Add KubeVirt’s documented TEE slop to guest memory when
`launchSecurity` is set (SEV family accounts ~256Mi in
virt-launcher overhead). Do not pretend CC is density-neutral.

### 16.7 What “working” means

Not “the VMI has launchSecurity in the spec.” All of:

1. `virsh dumpxml` in virt-launcher shows `launchSecurity type=sev-snp|tdx`
2. Guest dmesg + TEE device node exist
3. Guest produces a report/quote bound to a nonce from the agent
4. That evidence verifies **off the hypervisor**
5. User Pod still does logs/exec through the shim

Until (3) and (4) pass, do not call the mode done.

## 17. Operator install changes

`pkg/maroonedpods-operator/resources/` must start deploying:

- namespace `marooned-system`
- RuntimeClass
- shim DaemonSet
- adaptor (can stay inside maroonedpods-controller)
- webhook configs updated for RuntimeClass
- RBAC: controller creates VMIs in marooned-system; shim reads Pods
- sandbox image pull secrets if needed

Do not deploy a KubeVirt CR.

## 17. Phased delivery (implement in this order)

### Phase 0 — skeleton

- Config fields, RuntimeClass manifest, infra namespace
- Webhook strips devices/PVCs, adds finalizer
- Empty shim that returns Unimplemented
- Tests: webhook unit tests

### Phase 1 — hidden VMI + l2bridge + pool

- Adaptor creates/claims sandbox VMI from a **pause** container Pod
- Guest boots, agent answers vsock ping
- `kubectl get vmi -n marooned-system` shows claimed VMI
- `kubectl get vmi -n <app>` is empty
- Pool refill

Done when: a RuntimeClass pause pod reaches Running and guest ping works.

### Phase 2 — one container, no PVC

- Shim CreateContainer → agent execs process from sandbox root or
  ImageVolume
- `kubectl logs` and `kubectl exec` work
- Image: nginx or busybox

Done when: `kubectl run --runtime-class=marooned` works end to end.

### Phase 3 — native storage

- RWO block + filesystem PVC as virtio-blk
- volumeMount paths honored in guest
- No virtiofs except explicit RWX test marked optional

### Phase 4 — devices

- Hugepages translation
- One SR-IOV resource annotation path (non-TEE only)
- DRA only if KubeVirt in the test cluster has the gate
- Translator rejects TEE + SR-IOV

### Phase 5 — polish

- Warm pool per node / size class / tee
- Guest IP / EndpointSlice for Services
- Metrics: claim latency, cold vs warm, agent errors

### Phase 6 — confidential compute

- Config + Pod annotation `maroonedpods.io/tee`
- UEFI sandbox-tee image; no kernelBoot on that path
- launchSecurity + nodeSelector + host-passthrough
- Agent `Attest(nonce)`
- SNP: snpguest report + off-host verify
- TDX: quote via QGS mounted into virt-launcher; off-host / Trustee verify
- Optional Trustee attester in guest
- Document QGS/PCCS/HCO jsonpatch as cluster pre-req, not operator payload

Done when the section 16.7 checklist passes on a lab node.

## 18. Tests

- Unit: `pkg/sandbox/translate` every row in section 10
- Unit: pool claim races (two pods, one VMI)
- Unit: webhook strip/keep matrix
- Functional (existing Ginkgo tree under `tests/`):
  - RuntimeClass busybox, logs, exec
  - hidden VMI namespace assertion
  - PVC RWO write/read
  - delete returns VMI to pool
- Do not require OpenShift CUDN for the default functional suite;
  use masquerade there. Add an OpenShift e2e later for l2bridge.
- CC tests are opt-in (`// +build tee` or a Ginkgo label `tee`).
  Skip when nodes lack `kubevirt.io/sev` / `kubevirt.io/tdx`.
  Never verify a report on the same node that ran the VMI.

## 19. Reuse vs replace in this repo

Reuse:

- operator SDK layout, cert rotation, CRD gen
- MaroonedPodsConfig plumbing
- warm-pool state machine idea
- VMI create client via `kubevirt.io/client-go`
- Makefile / cluster-up / kubevirtci

Replace / do not call from sandbox path:

- gate controller
- node image + cloud-init k3s join
- taint / nodeSelector injection
- “wait for Node Ready then ungate”

## 20. Implementation rules for the coding agent

- Match existing Go style in `pkg/maroonedpods-controller`.
- No new dependencies unless needed for gRPC/vsock.
- Do not vendor Kata. Trustee attester in the guest image is allowed;
  do not run Trustee AS on the hypervisor node.
- Do not “temporarily” run the user Pod as privileged.
- Do not create VMIs in the user namespace.
- Do not modify virt-controller or KubeVirt source in this repo.
- If KubeVirt APIs are missing (ImageVolume, DRA), feature-detect
  and Event; do not crash the controller.
- Update README with sandbox mode as the primary story; move node
  mode to `docs/node-mode.md`.
- Commit in phase-sized PRs if working on a branch; do not mix
  shim protocol and operator CRD gen in one giant diff if avoidable.

## 21. First command sequence for a build session

1. Read `pkg/maroonedpods-controller/maroonedpods-gate-controller/` and
   `staging/src/maroonedpods.io/api/` so changes match style.
2. Implement Phase 0 config + webhook.
3. Implement VMI template + adaptor claim (Phase 1) with a hardcoded
   sandbox image name.
4. Only then scaffold `cmd/marooned-shim` and `cmd/marooned-agent`.
5. Do not start `images/node` changes.
6. Do not start Phase 6 until Phase 2 works on a non-TEE VMI.
   CC is a VMI profile + guest tools, not a new object model.

## 22. Acceptance snapshot (end of Phase 2)

```
kubectl apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: isolated-busybox
spec:
  runtimeClassName: marooned
  containers:
  - name: box
    image: busybox
    command: ["sleep", "3600"]
EOF

kubectl wait pod/isolated-busybox --for=condition=Ready --timeout=120s
kubectl exec isolated-busybox -- echo ok
kubectl logs isolated-busybox
kubectl get vmi -n marooned-system   # 1 claimed
kubectl get vmi                      # 0 in default
```

That is the only demo that counts for v1.
