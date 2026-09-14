# Sandbox mode

Users write a Pod with `runtimeClassName: marooned`. The container runs in a
hidden KubeVirt guest. `kubectl logs` / `exec` / probes / Services target that
Pod. QEMU, CSI, SR-IOV, DRA, and hugepages stay on virt-launcher.

**Hidden means the user did not author the VMI**, not “other namespace”.

| Kind of sandbox | VMI namespace |
|---|---|
| Pod has a PVC / ephemeral / block volume that must be *that* claim | same as the Pod (`marooned-<pod-uid>`) |
| Diskless (no such volume) | `marooned-system` (warm pool allowed) |
| Operator, shim, RuntimeClass, config | `marooned-system` |

`kubectl get vmi` in the application namespace for a PVC pod is expected.
Cross-namespace PVC attach is out of scope: Kubernetes will not bind a claim
in `app` onto a VMI in `marooned-system`, and a CDI clone would be a second
disk, not the user’s.

The webhook still **strips** PVCs from the user Pod so kubelet does not
NodePublish them into the shim. virt-launcher in the **pod** namespace mounts
the claim. The user Pod does not.

## RuntimeClass

The operator installs:

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

`handler: marooned` must match the node runtime config:

```
# containerd
[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.marooned]
  runtime_type = "io.containerd.marooned.v1"
```

The node DaemonSet `marooned-shim` in `marooned-system` serves a CRI subset on
`/var/run/marooned/cri.sock`. Point the containerd/CRI-O marooned handler at
that shim. Do not run a second kubelet.

## Networking

Production: primary UDN/CUDN plus `binding.name: l2bridge` and
`networks: [{ name: default, pod: {} }]`.

Diskless sandboxes in `marooned-system` need that namespace selected by the
same CUDN the application namespace uses. PVC sandboxes put the VMI in the
app namespace, which is the honest primary-UDN path. SR-IOV is a second
interface, never a replacement for l2bridge.

Dev fallback: `sandbox.network.binding: masquerade`. Guest IP is not the
Service IP. The functional suite uses masquerade so it can run without OVN-K.

The adaptor publishes guest IPs on an EndpointSlice owned for the Pod. It does
not fight kubelet for `status.podIP`.

## CPU / memory double-count

v1 leaves cpu/memory **requests** on the user Pod (scheduler fairness) and also
sizes the hidden VMI from those requests plus `extraGuestOverhead`. Devices,
hugepages, and PVCs are stripped from the user Pod. The original volume list
is stored on `maroonedpods.io/volumes` and `maroonedpods.io/placement=user|infra`
so the adaptor can reconstruct the VMI disks.

## Confidential compute

Same RuntimeClass. Annotate the Pod:

```yaml
metadata:
  annotations:
    maroonedpods.io/tee: snp   # or tdx
spec:
  runtimeClassName: marooned
```

TEE guests boot UEFI (no kernelBoot), `secureBoot: false`. TDX also sets
`features.smm.enabled: false`. Evidence is produced in the guest and verified
off the hypervisor. Maroonedpods is not a verifier.

SNP + SR-IOV / GPU / virtiofs RWX is rejected. Hugepages + TEE is allowed.

Do not install a second KubeVirt CR. Enable `WorkloadEncryptionSEV` /
`WorkloadEncryptionTDX` on the existing KubeVirt/HCO object.
