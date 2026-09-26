# Sandbox mode

Users write a Pod with `runtimeClassName: marooned`. The container runs in a
hidden KubeVirt guest. `kubectl logs` / `exec` / probes / Services target that
Pod. QEMU, CSI, SR-IOV, DRA, and hugepages stay on virt-launcher.

**Hidden means the user did not author the VMI**, not “other namespace”.

| Kind of sandbox | VMI namespace |
|---|---|
| Any RuntimeClass=marooned Pod | same as the Pod (`marooned-<pod-uid>`) |
| Operator, shim, RuntimeClass | `marooned-system` |

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
    memory: 256Mi
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

## CPU / memory and cgroups

There are **two** cgroups. We do not put qemu in the user Pod’s cgroup
(Kata’s “one cgroup”). virt-launcher owns QEMU; joining it to the pause
cgroup would fight KubeVirt.

| Object | What it is | What accounts it |
|---|---|---|
| User Pod | CRI-O pause + RuntimeClass `podFixed` (`100m` + `256Mi`) + the Pod’s own cpu/memory **requests** | Scheduler, `kubectl top`, HPA, eviction |
| virt-launcher | QEMU + guest RAM (`requests` + `extraGuestOverhead`) | The real node RAM/CPU for the VM |

`kubectl top` follows the **pause** cgroup, not the guest. CRI stats
(`marooned-oci events --stats`) report guest RSS; kubelet does not use
them for HPA until CRI-O calls the runtime stats path.

Set the serving Pod’s `resources.requests/limits` to what the **guest
workload** needs (vLLM, Ollama). That is what sizes the VMI. The
RuntimeClass tax is extra, on purpose, so the node is not surprised by
qemu.

Do not subtract qemu from the user request to “avoid double-count.”
The scheduler must see both the guest and the virt-launcher tax.

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

SNP + SR-IOV / GPU is rejected. Hugepages + TEE is allowed. RWX PVCs attach as virtio-blk; we do not use virtiofs (virt-launcher stays non-root).

Do not install a second KubeVirt CR. Enable `WorkloadEncryptionSEV` /
`WorkloadEncryptionTDX` on the existing KubeVirt/HCO object.
