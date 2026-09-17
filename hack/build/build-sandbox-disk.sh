#!/usr/bin/env bash
# Build KubeVirt images for the sandbox guest (run on the host, then podman push):
#   marooned-kernel  — vmlinuz + initrd (firmware.kernelBoot.container)
#   marooned-sandbox — ext4 qcow2 of alpine + marooned-agent (containerDisk)
#
#   ./hack/build/build-sandbox-disk.sh
#   podman build -t $DOCKER_PREFIX/marooned-sandbox:latest -f _out/sandbox-disk/Dockerfile.sandbox _out/sandbox-disk
#   podman build -t $DOCKER_PREFIX/marooned-kernel:latest  -f _out/sandbox-disk/Dockerfile.kernel  _out/sandbox-disk
set -euo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd -P)"
out="${root}/_out/sandbox-disk"
mkdir -p "${out}/rootfs" "${out}/kernel"
export GO111MODULE="${GO111MODULE:-on}"
export GOFLAGS="${GOFLAGS:--mod=vendor}"

build_agent() {
  echo "building marooned-agent"
  local goflags=(-mod=vendor)
  if [ -n "${GO:-}" ]; then
    ( cd "${root}" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 "$GO" build "${goflags[@]}" -o "${out}/marooned-agent" ./cmd/marooned-agent )
    return
  fi
  if command -v go >/dev/null 2>&1; then
    ( cd "${root}" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build "${goflags[@]}" -o "${out}/marooned-agent" ./cmd/marooned-agent )
    return
  fi
  if [ -n "${GOROOT:-}" ] && [ -x "${GOROOT}/bin/go" ]; then
    ( cd "${root}" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 "${GOROOT}/bin/go" build "${goflags[@]}" -o "${out}/marooned-agent" ./cmd/marooned-agent )
    return
  fi
  local cri=""
  command -v podman >/dev/null 2>&1 && cri=podman
  command -v docker >/dev/null 2>&1 && cri="${cri:-docker}"
  if [ -n "$cri" ]; then
    echo "host go not found; building agent with ${cri} golang:1.19-alpine"
    $cri run --rm -v "${root}:/src:Z" -w /src golang:1.19-alpine \
      sh -c 'CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=vendor -o /src/_out/sandbox-disk/marooned-agent ./cmd/marooned-agent'
    return
  fi
  echo "go not found. Install Go, set GO=/path/to/go, or install podman/docker." >&2
  exit 1
}
build_agent()

echo "fetching alpine minirootfs + linux-lts"
curl -fsSL -o "${out}/minirootfs.tgz" \
  https://dl-cdn.alpinelinux.org/alpine/v3.18/releases/x86_64/alpine-minirootfs-3.18.6-x86_64.tar.gz
apk_index="$(curl -fsSL https://dl-cdn.alpinelinux.org/alpine/v3.18/main/x86_64/APKINDEX.tar.gz | tar -xzO APKINDEX | grep -A1 '^P:linux-lts$' | grep '^V:' | head -1 | cut -d: -f2)"
curl -fsSL -o "${out}/linux-lts.apk" \
  "https://dl-cdn.alpinelinux.org/alpine/v3.18/main/x86_64/linux-lts-${apk_index}.apk"

rm -rf "${out}/rootfs" "${out}/kpkg"
mkdir -p "${out}/rootfs" "${out}/kpkg" "${out}/kernel"
tar -C "${out}/rootfs" -xzf "${out}/minirootfs.tgz"
chmod -R u+w "${out}/rootfs"
mkdir -p "${out}/rootfs/usr/local/bin"
tar -C "${out}/kpkg" -xzf "${out}/linux-lts.apk" 2>/dev/null || tar -C "${out}/kpkg" -xf "${out}/linux-lts.apk"
find "${out}/kpkg" -name 'vmlinuz*' | head -1 | xargs -I{} cp {} "${out}/kernel/vmlinuz"
cp "${out}/marooned-agent" "${out}/rootfs/usr/local/bin/marooned-agent"
chmod +x "${out}/rootfs/usr/local/bin/marooned-agent"
rm -f "${out}/rootfs/sbin/init"
cat > "${out}/rootfs/sbin/init" << 'INIT'
#!/bin/sh
export PATH=/usr/sbin:/usr/bin:/sbin:/bin
mount -t proc proc /proc 2>/dev/null || true
mount -t sysfs sysfs /sys 2>/dev/null || true
mount -t devtmpfs devtmpfs /dev 2>/dev/null || true
mkdir -p /dev/pts /run /tmp
ip link set lo up 2>/dev/null || true
ip link set eth0 up 2>/dev/null || true
udhcpc -i eth0 -n -q -t 8 2>/dev/null || true
exec /usr/local/bin/marooned-agent -listen tcp://0.0.0.0:1024
INIT
chmod +x "${out}/rootfs/sbin/init"

echo "packing virtio+ext4 initramfs"
ird="${out}/initrd-root"
rm -rf "$ird"
mkdir -p "$ird"/{bin,dev,proc,sys,newroot,lib/modules}
cp "${out}/rootfs/bin/busybox" "$ird/bin/busybox"
chmod +x "$ird/bin/busybox"
modroot=$(find "${out}/kpkg" -type d -name '6.*-lts' | head -1)
copy_mod() {
  local n="$1"
  local f
  f=$(find "${modroot}" -name "${n}.ko.gz" | head -1)
  [ -n "$f" ] || return 0
  gzip -dc "$f" > "$ird/lib/modules/${n}.ko"
}
for m in virtio virtio_ring virtio_pci virtio_pci_legacy_dev virtio_pci_modern_dev virtio_blk crc16 mbcache jbd2 ext4; do
  copy_mod "$m"
done
cat > "$ird/init" << 'IR'
#!/bin/busybox sh
BB=/bin/busybox
$BB mount -t proc proc /proc
$BB mount -t sysfs sys /sys
$BB mount -t devtmpfs dev /dev
for m in virtio virtio_ring virtio_pci_legacy_dev virtio_pci_modern_dev virtio_pci virtio_blk crc16 mbcache jbd2 ext4; do
  [ -f /lib/modules/${m}.ko ] && $BB insmod /lib/modules/${m}.ko
done
$BB sleep 1
$BB mount -t ext4 /dev/vda /newroot || $BB mount -t ext4 /dev/vda1 /newroot
exec $BB switch_root /newroot /sbin/init
IR
chmod +x "$ird/init"
( cd "$ird" && find . | cpio -o -H newc 2>/dev/null | gzip -9 > "${out}/kernel/initrd" )

echo "creating ext4 qcow2"
rm -f "${out}/disk.raw" "${out}/disk.qcow2"
truncate -s 512M "${out}/disk.raw"
mke2fs -t ext4 -d "${out}/rootfs" -E root_owner=0:0 -F "${out}/disk.raw"
qemu-img convert -f raw -O qcow2 "${out}/disk.raw" "${out}/disk.qcow2"

cp "${out}/kernel/vmlinuz" "${out}/vmlinuz"
cp "${out}/kernel/initrd" "${out}/initrd"
cat > "${out}/Dockerfile.sandbox" << 'DF'
FROM scratch
COPY disk.qcow2 /disk/disk.qcow2
DF
cat > "${out}/Dockerfile.kernel" << 'DF'
FROM alpine:3.18
COPY vmlinuz /boot/vmlinuz
COPY initrd /boot/initrd
DF
ls -lh "${out}/disk.qcow2" "${out}/vmlinuz" "${out}/initrd"
echo "ok ${out}"
echo "next:"
echo "  podman build -t \$DOCKER_PREFIX/marooned-sandbox:latest -f ${out}/Dockerfile.sandbox ${out}"
echo "  podman build -t \$DOCKER_PREFIX/marooned-kernel:latest  -f ${out}/Dockerfile.kernel  ${out}"
echo "  podman push --tls-verify=false \$DOCKER_PREFIX/marooned-sandbox:latest"
echo "  podman push --tls-verify=false \$DOCKER_PREFIX/marooned-kernel:latest"
