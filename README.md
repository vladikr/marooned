# MaroonedPods

VM-level isolation for Kubernetes Pods using KubeVirt. You write a **Pod**.
The container runs in a hidden guest. QEMU, CSI, SR-IOV, DRA, and hugepages
stay on virt-launcher.

```yaml
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
```

That is the v1 interface. There is no user-authored VMI and no guest kubelet.

## How it works

```
User Pod  (app ns)     runtimeClassName: marooned
   │  kubectl logs / exec / probes / Services
   ▼
marooned-shim (DaemonSet on the worker, CRI handler)
   │  vsock or tcp
   ▼
marooned-agent (inside the guest)
   ▲
   │  claim or create
maroonedpods-controller adaptor
   ▼
VMI + virt-launcher
   diskless:  marooned-system
   with PVC:  same namespace as the Pod (marooned-<pod-uid>)
```

The webhook strips devices, hugepages, and PVCs from the **user** Pod so kubelet
does not publish them twice. CPU and memory requests stay on the Pod for
scheduling. The original volume list is stored on
`maroonedpods.io/volumes` and `maroonedpods.io/placement=user|infra`.

This repository is **sandbox only**. A `maroonedpods.io/maroon` label does
nothing here. Guest-kubelet / VM-is-a-Node isolation (including group mode)
is https://github.com/vladikr/maroonedpods.

Do **not** install this operator and maroonedpods in the same cluster. They
share `MaroonedPodsConfig`.

Sandbox networking, CUDN/l2bridge, PVC placement, and confidential compute:
[docs/sandbox-mode.md](docs/sandbox-mode.md).

---

## Prerequisites

- Kubernetes 1.26+
- **One** existing KubeVirt install (do not create a second KubeVirt or HCO CR)
- `kubectl` pointed at that cluster
- On every worker that should run these pods: a `marooned` runtime handler
  (see [Runtime handler](#runtime-handler) below)

Optional:

- Primary UDN/CUDN for production `l2bridge` networking
- Without that, set `sandbox.network.binding: masquerade` (dev / kubevirtci)

---

## Deploy

### 1. Install the operator

**From this tree** (sandbox mode is in-tree; use this until a release includes it):

```bash
# kubevirtci cluster with KubeVirt already installed by cluster-up
make cluster-up
make cluster-sync
```

`cluster-sync` builds images, generates manifests, installs the operator, and
applies the `MaroonedPods` CR. It waits until the operator reports Available.

**On an existing cluster**, after building and pushing images:

```bash
make manifests
kubectl apply -f _out/manifests/release/maroonedpods-operator.yaml
kubectl apply -f _out/manifests/release/maroonedpods-cr.yaml
```

The operator then deploys:

- CRDs (`MaroonedPods`, `MaroonedPodsConfig`)
- RuntimeClass `marooned`
- namespace `marooned-system`
- webhook (mutating/validating)
- `maroonedpods-controller` (includes the sandbox adaptor)
- `marooned-shim` DaemonSet in `marooned-system`

Do **not** install a second KubeVirt CR.

Node-mode (guest kubelet) install is the maroonedpods project, not this one.

### 2. Runtime handler

`RuntimeClass.handler: marooned` must exist in containerd or CRI-O on every
worker. The shim listens on `/var/run/marooned/cri.sock`.

containerd (`/etc/containerd/config.toml`):

```toml
[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.marooned]
  runtime_type = "io.containerd.marooned.v1"
```

Then restart containerd. Point that runtime at the marooned shim; do not run a
second kubelet.

### 3. Sandbox config

```bash
kubectl apply -f examples/maroonedpods-config.yaml
```

Minimum:

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
    warmPoolSize: 2
    network:
      binding: masquerade   # use l2bridge when the cluster has primary UDN
    confidentialCompute:
      default: off
```

Confirm the control plane is up:

```bash
kubectl get maroonedpods
kubectl get maroonedpodsconfig
kubectl get runtimeclass marooned
kubectl get ds -n marooned-system
kubectl get pods -n maroonedpods
```

---

## Use

### Diskless pod

```bash
kubectl apply -f examples/sandbox-pod.yaml
kubectl wait pod/isolated-busybox --for=condition=Ready --timeout=120s
kubectl exec isolated-busybox -- echo ok
kubectl logs isolated-busybox
```

What you should see:

```bash
kubectl get vmi -n marooned-system   # one claimed sandbox VMI
kubectl get vmi                      # none in default
```

### Pod with a PVC

A claim in namespace `app` cannot attach to a VMI in `marooned-system`.
Those sandboxes create the VMI **next to the Pod**.

```bash
kubectl apply -f examples/sandbox-pod-pvc.yaml
kubectl wait pod/isolated-pvc --for=condition=Ready --timeout=180s
kubectl exec isolated-pvc -- sh -c 'echo x > /data/hi && cat /data/hi'
```

```bash
kubectl get vmi                    # marooned-<pod-uid> in this namespace
kubectl get vmi -n marooned-system # that VMI is not serving isolated-pvc
```

The user Pod still has no PVC (webhook stripped it). virt-launcher in the **app**
namespace has the claim mounted.

### Confidential compute (SNP / TDX)

Same RuntimeClass. Annotate the Pod; do not add a second handler.

```yaml
metadata:
  annotations:
    maroonedpods.io/tee: snp    # or tdx
spec:
  runtimeClassName: marooned
```

Enable `WorkloadEncryptionSEV` / `WorkloadEncryptionTDX` on the **existing**
KubeVirt/HCO object. Evidence is produced in the guest and verified off the
hypervisor. TEE + SR-IOV is rejected. See [docs/sandbox-mode.md](docs/sandbox-mode.md).

---

## Configuration reference

| Field | Meaning |
|---|---|
| `spec.defaultMode` | `Sandbox` (RuntimeClass) or `Node` (legacy guest kubelet) |
| `spec.sandbox.infraNamespace` | Hidden diskless VMIs (`marooned-system`) |
| `spec.sandbox.rootfsImage` | Guest agent OS (not the k3s node image) |
| `spec.sandbox.warmPoolSize` | Diskless pool per `(node, size class, tee)` |
| `spec.sandbox.network.binding` | `l2bridge` (prod) or `masquerade` (dev) |
| `spec.sandbox.confidentialCompute.default` | `off` / `snp` / `tdx` / `annotation` |

PVC pods never claim the infra pool. They always cold-create in the Pod
namespace.

---

## Development

```bash
make build          # controller, operator, server, shim, agent
make test WHAT='./pkg/webhook ./pkg/sandbox/... ./pkg/maroonedpods-server/handler'
make cluster-up     # kubevirtci + KubeVirt
make cluster-sync   # deploy from this tree
make functest
make cluster-down
```

Images:

```bash
make build-sandbox-image   # guest agent OS
```

---

## License

Apache 2.0. See [LICENSE](LICENSE).
