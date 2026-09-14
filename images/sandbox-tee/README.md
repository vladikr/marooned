# Sandbox TEE image

UEFI-bootable containerDisk for SNP/TDX sandbox guests (Phase 6).

Requirements:

- Guest kernel with `CONFIG_SEV_GUEST` and/or TDX guest support
- `/dev/sev-guest` or `/dev/tdx_guest`
- `marooned-agent` plus `snpguest` / TDX quote helper
- Optional `trustee-attester` when `sandbox.confidentialCompute.trustee.enabled` is true
- Secure Boot off

This image must not contain kubelet, k3s, or a node CNI.

Do not start this image until a non-TEE sandbox pod works (Phase 2).
