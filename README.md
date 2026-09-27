# Marooned

VM isolation for Kubernetes Pods, using KubeVirt. You write a **Pod** with
`runtimeClassName: marooned`. The container runs in a guest. QEMU, CSI, and
devices stay on virt-launcher. There is no user-authored VMI and no guest
kubelet.

The Pod is the API. The VMI is isolation.

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: isolated
spec:
  runtimeClassName: marooned
  containers:
  - name: box
    image: quay.io/prometheus/busybox:latest
    imagePullPolicy: Never
    command: ["sleep", "3600"]
```

```bash
kubectl apply -f examples/sandbox-pod.yaml
kubectl get pods --watch          # wait until Ready 1/1
kubectl exec isolated-busybox1 -- cat /scratch/ok   # vol-ok
kubectl logs isolated-busybox1
kubectl delete -f examples/sandbox-pod.yaml         # VMI goes with the Pod
```

Do not `kubectl` the VMI. `kubectl get vmi` in the app namespace showing
`marooned-<pod-uid>` is expected.

This repo is **sandbox only** (Kata-shaped: one Pod → one VMI). Guest-kubelet /
VM-is-a-Node is [vladikr/maroonedpods](https://github.com/vladikr/maroonedpods).
Do **not** install both operators; they share `MaroonedPodsConfig`.

Details: [docs/sandbox-mode.md](docs/sandbox-mode.md). Follow-ups:
[hack/sandbox-followups.md](hack/sandbox-followups.md).

---

## How it works

```
User Pod  (app ns)     runtimeClassName: marooned
   │  kubectl logs / exec / probes / Services / top
   ▼
CRI-O handler marooned → marooned-oci (pause on the host)
   │  guest-start
   ▼
marooned-shim (DaemonSet) ── vsock ── marooned-agent (in the guest)
   ▲
   │  creates / deletes
maroonedpods-controller adaptor
   ▼
VMI marooned-<pod-uid> + virt-launcher  (same namespace as the Pod)
```

The webhook strips devices, hugepages, and PVCs from the **user** Pod so
kubelet does not publish them twice. CPU/memory **requests** stay on the Pod
for scheduling. Volumes are snapshotted on `maroonedpods.io/volumes` and
attached on the VMI (virtio-blk). emptyDir is a guest tmpfs.

v1 runtime is **CRI-O** (`marooned-oci` + conmon). containerd is not wired
([#14](https://github.com/vladikr/marooned/issues/14)).

---

## Prerequisites

- Kubernetes 1.26+ (kubevirtci pin: `k8s-1.37`)
- **One** existing KubeVirt install (do not create a second KubeVirt or HCO CR)
- `kubectl` pointed at that cluster
- On workers: CRI-O handler `marooned` (see [Runtime handler](#2-runtime-handler))

Optional:

- Primary UDN/CUDN for production `l2bridge`
- Without that: `sandbox.network.binding: masquerade` (kubevirtci)

---

## Deploy

### 1. Install the operator

Default: **this repo’s kubevirtci**. `cluster-up` starts the VMs and installs
**one** KubeVirt CR from `KUBEVIRT_RELEASE` (not your local kubevirt tree).

```bash
export KUBEVIRT_MEMORY_SIZE=9216M
export KUBEVIRT_PROVIDER=k8s-1.37
export KUBEVIRT_RELEASE=latest_stable
make cluster-up
make cluster-sync
make cluster-push
./hack/kubevirtci-install-crio-handler.sh
```

Do not also `make cluster-up` from kubevirt against the same VMs.

**On an existing cluster**, after building and pushing images:

```bash
make manifests
kubectl apply -f _out/manifests/release/maroonedpods-operator.yaml
kubectl apply -f _out/manifests/release/maroonedpods-cr.yaml
```

The operator deploys CRDs, RuntimeClass `marooned`, namespace
`marooned-system`, webhook, `maroonedpods-controller` (sandbox adaptor),
`marooned-shim` DaemonSet, and `maroonedpods-server`.

### 2. Runtime handler

v1 is CRI-O. After the shim DaemonSet is running:

```bash
./hack/kubevirtci-install-crio-handler.sh
```

That installs `marooned-oci` and `hack/crio/20-marooned.conf`. Until it runs
(and after `cluster-push` if CRI-O bounced without reloading), the Pod stays
**ContainerCreating** even if the VMI is Running:
`failed to find runtime handler marooned`.

### 3. Sandbox config

```bash
kubectl apply -f examples/maroonedpods-config.yaml
```

```yaml
apiVersion: maroonedpods.io/v1alpha1
kind: MaroonedPodsConfig
metadata:
  name: default
spec:
  defaultMode: Sandbox
  sandbox:
    infraNamespace: marooned-system
    rootfsImage: quay.io/vladikr/marooned-sandbox:latest
    kernelBoot:
      image: quay.io/vladikr/marooned-kernel:latest
      kernelPath: /boot/vmlinuz
      initrdPath: /boot/initrd
      kernelArgs: "root=/dev/vda rootfstype=ext4 rw console=ttyS0"
    network:
      binding: masquerade   # l2bridge when the cluster has primary UDN
    confidentialCompute:
      default: off
```

```bash
kubectl get maroonedpodsconfig
kubectl get runtimeclass marooned
kubectl get ds -n marooned-system
kubectl get pods -n maroonedpods
```

---

## Use

Examples under `examples/` (all `runtimeClassName: marooned`):

| File | What |
|---|---|
| `sandbox-pod.yaml` | busybox httpd, emptyDir `/scratch` |
| `sandbox-pod-pvc.yaml` | filesystem PVC at `/data` |
| `sandbox-pod-block.yaml` | `volumeDevices` raw disk, no mkfs |
| `sandbox-pod-multi.yaml` | init + app, shared emptyDir |
| `sandbox-nginx.yaml` | stock nginx |
| `sandbox-python.yaml` | CPython + emptyDir |
| `sandbox-model.yaml` | engine image + **weights PVC** (vLLM/Ollama shape) |
| `sandbox-metrics-apiservice.yaml` | `kubectl top` from guest RSS |

```bash
kubectl apply -f examples/sandbox-pod.yaml
kubectl wait --for=condition=Ready pod/isolated-busybox1 --timeout=180s
kubectl exec isolated-busybox1 -- cat /scratch/ok
```

**Images:** kubelet pulls as usual. We copy that unpacked tree into a sized
guest disk (not a second Hub pull). Guest-pull is fallback. Do not put 70B
weights in the image; use a data PVC (`sandbox-model.yaml`).

**Network (masquerade / kubevirtci):** `status.podIP` is the pause CNI address.
The pause forwards container ports to the guest, so `curl $PODIP:8080` works.
Services use an adaptor-owned EndpointSlice. Production: `l2bridge` + CUDN;
EndpointSlice uses the guest IP. Do not enable l2bridge without OVN-K.

**kubectl top:** apply `examples/sandbox-metrics-apiservice.yaml` if
metrics-server is not already registered. Guest RSS (not the pause cgroup).
Idle CPU may be `0m`.

**TEE:** same RuntimeClass; annotate `maroonedpods.io/tee: snp` or `tdx`.
Enable the matching gate on the **existing** KubeVirt/HCO object. See
[docs/sandbox-mode.md](docs/sandbox-mode.md).

---

## Configuration

| Field | Meaning |
|---|---|
| `spec.defaultMode` | `Sandbox` in this repo |
| `spec.sandbox.infraNamespace` | `marooned-system` (shim, RuntimeClass) |
| `spec.sandbox.rootfsImage` | Guest OS + agent (not the user image) |
| `spec.sandbox.network.binding` | `l2bridge` (prod) or `masquerade` (dev) |
| `spec.sandbox.confidentialCompute.default` | `off` / `snp` / `tdx` / `annotation` |

The VMI is always `marooned-<pod-uid>` in the **Pod** namespace.

---

## Development

```bash
make build
make test WHAT='./pkg/webhook ./pkg/sandbox/... ./pkg/maroonedpods-server/handler'
make cluster-up
make cluster-sync
make cluster-push
./hack/kubevirtci-install-crio-handler.sh
```

Vendored kubevirtci is tag `2609091017-ad4877a9` (`hack/update-kubevirtci.sh`).
Do not inherit providers from `~/devel/kubevirt` unless you set `KUBEVIRT_DIR`.

```bash
export KUBEVIRT_PROVIDER=k8s-1.37
make cluster-push
# SKIP_GUEST_DISK=1 make cluster-push   # operator/shim only; not for agent changes
./hack/kubevirtci-install-crio-handler.sh   # after a CRI-O bounce; also refreshes sandbox:latest
```

Demo (asciinema): `bash examples/sandbox-demo.sh`.

---

## License

Apache 2.0. See [LICENSE](LICENSE).
