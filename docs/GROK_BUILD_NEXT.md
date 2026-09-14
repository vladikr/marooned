# Marooned sandbox — next Grok Build session

This is the increment spec. Read `GROK_BUILD.md` for the original constitution.
Do **not** restart Phases 0–2. Do **not** revive node mode. Do **not**
build a cross-namespace PVC proxy.

Workspace already contains the maroonedpods tree with sandbox mode landed
through Phase 2 (unit tests + five binaries). Continue in that tree.

---

## 0. What is already true (do not regress)

- Pod + `runtimeClassName: marooned` → webhook mutates → adaptor
  claims/creates a VMI → shim ↔ agent.
- Gate controller only for `maroonedpods.io/maroon=true`.
- Translator: CPU/memory, hugepages, SR-IOV, PVC-as-virtio-blk *names*,
  emptyDir tmpfs, l2bridge/masquerade.
- TEE + SR-IOV rejected. TEE uses UEFI, not kernelBoot.
- `Attest` is a stub. Live cluster e2e is not done.
- PVC *translation* exists. PVC *attach* across namespaces does not.
  That is the point of this session.

## 1. Decision that changes placement

A PVC in namespace `app` cannot be live-attached to a VMI in
`marooned-system`. Kubernetes will not do that. CDI clone is a second
volume, not the user’s disk. Do not implement clone as the default.

**Hidden means “user did not author the VMI”, not “other namespace”.**

| Kind of sandbox | VMI namespace | Why |
|---|---|---|
| Pod has any PVC / ephemeral / block volume that must be *that* claim | **same as the Pod** | CSI + virtio-blk are native |
| Diskless (no such volume) | `marooned-system` pool still allowed | no claim to attach |
| Operator, shim, RuntimeClass, config | `marooned-system` | unchanged |

virt-launcher always lives next to the VMI. That is how KubeVirt
schedules CSI, hugepages, DRA, l2bridge. Same-ns VMI is also the
honest CUDN/primary-UDN path.

`kubectl get vmi -n app` will show `marooned-<pod-uid>` for PVC pods.
Document that. Do not hide it with a clone.

## 2. What to change in the existing code

### 2.1 Placement policy (`pkg/sandbox/adaptor` + translator)

Add a single function and use it everywhere:

```
NeedsUserNamespace(pod) bool
  true if the Pod (before or after webhook strip — use the
  original annotation snapshot if the webhook already stripped
  volumes) has:
    - persistentVolumeClaim
    - ephemeral CSI
    - imageVolume that KubeVirt will attach as a disk from a
      namespaced object
  false otherwise
```

Webhook today strips PVCs from the user Pod. Persist enough
information to reconstruct the volume list:

- Annotation on the Pod, e.g. `maroonedpods.io/volumes` = JSON of
  the stripped `volumes` + `volumeMounts`, **or**
- Keep a copy on the VMI spec only and read the VMI.

Preferred: annotation on the Pod set by the webhook at admit time
(the adaptor must not depend on an unsynced informer of “what the
user originally wrote”). Size-limit the annotation; fail admit if
it cannot fit.

Then:

```
if NeedsUserNamespace(pod):
    create VMI in pod.Namespace
    name: marooned-<pod.UID>          # stable, DNS-1123
    ownerRef: Pod
    do NOT claim a marooned-system pool VMI
else:
    existing path: claim/create in marooned-system
```

Do not migrate a claimed pool VMI across namespaces. A pool VMI in
`marooned-system` cannot later grow `app/my-pvc`. If a diskless
pool VMI was claimed and you discover volumes — you should not:
webhook + annotation exist before bind. If you race, delete the
wrong VMI and create in-ns. Test that race.

### 2.2 Webhook

Keep:

- strip hugepages / devices / resourceClaims / workload PVCs from
  the **user** Pod so kubelet does not NodePublish into the shim
- keep cpu/memory
- finalizer `maroonedpods.io/sandbox-cleanup`
- no scheduling gate

Add:

- write `maroonedpods.io/volumes` (or equivalent) before strip
- write `maroonedpods.io/placement=user|infra` so the adaptor
  does not re-derive from a stripped spec

Do not leave PVCs on the user Pod. Two publishers is worse than a
visible VMI.

### 2.3 Translator (PVC path)

Same-ns VMI:

```
Pod volume PVC { claimName: foo }  →
  VMI.volumes[].persistentVolumeClaim.claimName: foo
  VMI.disks[] virtio-blk
```

No DataVolume unless the cluster already requires DV for that PVC
style. No `namespace:` field — it does not exist on the VMI volume
source.

Hotplug vs define-at-create:

- Cold create in user ns: put disks on the VMI spec at create.
- Do not create a diskless VMI and hotplug as the v1 path unless
  create-with-disks fails in tests. Create-with-disks is simpler
  and matches KubeVirt’s common case.

emptyDir stays guest tmpfs (no PVC). configMap/secret: extra small
disk or agent files; if you attach them as volumes, they are also
same-ns objects — same rule.

### 2.4 Warm pool

Pool key remains `(node, sizeClass, tee)` in `marooned-system`.

New rule:

- `NeedsUserNamespace == true` → **never claim from the pool**.
  Cold-create in the user namespace. Refill logic ignores these
  VMIs (`maroonedpods.io/pool-state` absent).
- Diskless pods still use the pool.

Do not implement “pool VM in user ns” in this session. That is a
later density trick and requires per-namespace pool pollution.

On delete of an in-ns VMI: delete it (ownerRef GC). Do not move it
to `marooned-system`.

### 2.5 RBAC

Controller SA must `create/get/update/delete` VMIs in **all
namespaces** (or a configured set), not only `marooned-system`.
Same for watching virt-launcher pods if you do that.

If current Role is namespaced to `marooned-system`, add a
ClusterRole for `virtualmachineinstances` and related kubevirt
resources. Do not grant the shim cluster-admin.

### 2.6 Docs

Update `docs/sandbox-mode.md` and README:

- Dual object is expected when the Pod has PVCs.
- `kubectl get vmi` in the app namespace is not a bug.
- Cross-namespace attach is explicitly out of scope.

## 3. What you will not do this session

- CDI clone / snapshot / “shadow PVC” in `marooned-system`
- Rebind a PV to a second PVC
- virtiofs of a host-published PVC
- Moving VMI spec merge back onto the user Pod
- Phase 6 attestation beyond what is already stubbed
  (do not block PVC work on Trustee)
- containerd ttrpc runtime v2 unless the PVC path is done and
  you still have time
- Node-mode changes

## 4. Implementation order

1. Webhook: persist stripped volume spec + placement annotation.
   Unit tests: strip still happens; annotation round-trips a PVC
   + two volumeMounts.
2. `NeedsUserNamespace` + adaptor branch.
   Unit tests:
   - diskless → infra ns, may claim pool
   - PVC pod → user ns, never claims pool
   - PVC pod never takes a `marooned-system` VMI even if the pool
     is full of available VMs
3. Translator emits same-ns PVC virtio-blk.
   Unit tests: claimName preserved, bus virtio, no namespace field.
4. RBAC ClusterRole.
5. Delete path: in-ns VMI gone with Pod; pool VMI still recycles.
6. Docs + `examples/sandbox-pod-pvc.yaml` (RWO filesystem PVC,
   mountPath `/data`).

Stop after that unless e2e cluster is available.

## 5. Acceptance

Unit (must pass before anything else):

```
webhook_test: volumes annotation present after admit
translate_test: PVC → virtio-blk claimName
adaptor_test: PVC pod VMI.namespace == pod.namespace
adaptor_test: diskless VMI.namespace == infraNamespace
pool_test: PVC pod does not decrement available pool
```

If a cluster exists:

```
kubectl apply -f examples/sandbox-pod-pvc.yaml
kubectl wait pod/isolated-pvc --for=condition=Ready --timeout=180s
kubectl exec isolated-pvc -- sh -c 'echo x > /data/hi && cat /data/hi'
kubectl get vmi -n <app>          # exactly one marooned-*
kubectl get vmi -n marooned-system
# that VMI is not the one serving isolated-pvc
```

Guest sees a block/fs mount at `/data`. virt-launcher in the **app**
namespace has the PVC mounted. The user Pod does not.

## 6. Follow-ups (ticket only, do not implement now)

- Warm pool of diskless VMIs + same-ns PVC hotplug after claim
  (only if create-in-ns latency is the problem)
- ImageVolume / guest-pull under TEE (Phase 6)
- containerd runtime v2 wiring on workers
- Live migration of in-ns sandbox VMI

## 7. Rules for the coding agent

- Match existing sandbox packages and test style.
- Do not rename RuntimeClass or finalizer.
- Do not create VMIs in `marooned-system` for PVC pods “just for
  now”.
- If KubeVirt rejects a PVC on a VMI (WaitForFirstConsumer,
  storage class, block vs fs), surface a Pod Event and requeue.
  Do not fall back to clone.
- If the webhook annotation exceeds size limits, fail admission
  with a clear error, do not silently drop volumes.
- Keep `GROK_BUILD.md` object-model table in sync if you touch it:
  VMI namespace is “pod ns if volumes, else marooned-system”.
