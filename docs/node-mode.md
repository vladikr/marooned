# Node mode (legacy)

Node mode is the original MaroonedPods path: a Pod with
`maroonedpods.io/maroon=true` is gated, a VirtualMachineInstance boots a
bootc+k3s guest kubelet, that guest joins as a Node, and the pod is then
scheduled onto it.

Sandbox mode (`runtimeClassName: marooned`) is the default. Do not use node
mode for new workloads.

## How it works

1. The admission webhook adds a scheduling gate, a pod-specific taint
   toleration, and a nodeSelector.
2. The gate controller creates a VMI in the **application namespace** using
   `spec.nodeImage`.
3. Cloud-init writes k3s join info. The guest starts `k3s-agent` and the
   node registers.
4. The controller removes the scheduling gate. The pod runs on that node.

## Configuration

Node-mode fields on `MaroonedPodsConfig`:

```yaml
spec:
  defaultMode: Node
  nodeImage: quay.io/vladikr/marooned-node:latest
  warmPoolSize: 0
  baseVMResources:
    cpu: 2
    memoryMi: 3072
  nodeTaintKey: maroonedpods.io
  resourceOverhead:
    cpu: 500m
    memory: 512Mi
```

## Example

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: isolated-nginx
  labels:
    maroonedpods.io/maroon: "true"
spec:
  containers:
  - name: nginx
    image: nginx:latest
```

## Why sandbox mode exists

Node mode pays for a guest kubelet, a Node object, and a join dance. QEMU,
CSI, and VFIO already live on virt-launcher; duplicating kubelet inside the
guest is the wrong default for Kata-like isolation.
