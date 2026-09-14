# Sandbox image

Minimal guest OS for RuntimeClass `marooned`. Contains the marooned-agent and
nothing that looks like a node: no kubelet, k3s, or CNI.

```
make build-sandbox-image
```

Non-TEE boots via `firmware.kernelBoot` from `sandbox.kernelBoot`. TEE guests
use the UEFI image under `images/sandbox-tee/`.
