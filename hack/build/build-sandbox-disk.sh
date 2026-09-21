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
build_agent

echo "fetching alpine minirootfs and linux-lts"
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
# vsock is insmod'd in initrd. Copying the full linux-lts module tree
# fills a 512M disk (~hundreds of MiB) so user rootfs unpack hits ENOSPC.
# mke2fs needs e2fsprogs-libs (libext2fs) and util-linux-libs (libblkid).
apkindex="${out}/APKINDEX"
curl -fsSL https://dl-cdn.alpinelinux.org/alpine/v3.18/main/x86_64/APKINDEX.tar.gz | tar -xzO APKINDEX >"$apkindex"
apk_ver() {
  awk -v p="$1" '$0=="P:"p {want=1} want && /^V:/ {print substr($0,3); exit}' "$apkindex"
}
extract_apk() {
  local pkg="$1" ver
  ver="$(apk_ver "$pkg")"
  [ -n "$ver" ] || { echo "no alpine package $pkg" >&2; return 1; }
  echo "fetching ${pkg}-${ver}"
  curl -fsSL -o "${out}/${pkg}.apk" \
    "https://dl-cdn.alpinelinux.org/alpine/v3.18/main/x86_64/${pkg}-${ver}.apk"
  tar -C "${out}/rootfs" -xzf "${out}/${pkg}.apk" 2>/dev/null || tar -C "${out}/rootfs" -xf "${out}/${pkg}.apk"
}
extract_apk e2fsprogs
extract_apk e2fsprogs-libs
extract_apk util-linux-libs || extract_apk libblkid || true
extract_apk util-linux-misc || extract_apk util-linux || true
extract_apk libcom_err || true
extract_apk libuuid || true
# musl loads from /lib; alpine apks often put .so files in /usr/lib
mkdir -p "${out}/rootfs/lib"
if [ -d "${out}/rootfs/usr/lib" ]; then
  find "${out}/rootfs/usr/lib" -maxdepth 1 -name '*.so*' -exec cp -a {} "${out}/rootfs/lib/" \;
fi
# apk metadata is not needed in the guest
rm -rf "${out}/rootfs/.PKGINFO" "${out}/rootfs/.SIGN"* "${out}/rootfs/.[A-Z]"* 2>/dev/null || true
ls -l "${out}/rootfs/lib"/libext2fs.so* "${out}/rootfs/lib"/libcom_err.so* "${out}/rootfs/lib"/libuuid.so* 2>/dev/null || true
rm -f "${out}/rootfs/sbin/init"
cat > "${out}/rootfs/sbin/init" << 'INIT'
#!/bin/sh
export PATH=/usr/sbin:/usr/bin:/sbin:/bin
mount -t proc proc /proc 2>/dev/null || true
mount -t sysfs sysfs /sys 2>/dev/null || true
mount -t devtmpfs devtmpfs /dev 2>/dev/null || true
mkdir -p /dev/pts /run /tmp
modprobe vsock 2>/dev/null || true
modprobe virtio_vsock 2>/dev/null || true
modprobe vmw_vsock_virtio_transport 2>/dev/null || true
ip link set lo up 2>/dev/null || true
ip link set eth0 up 2>/dev/null || true
udhcpc -i eth0 -n -q -t 8 2>/dev/null || true
# Agent must not be PID 1: a Go process that exits (panic, deadlock
# abort) is exit_group(2) and the kernel panics. busybox sh reaps and
# restarts the agent.
while true; do
  /usr/local/bin/marooned-agent -listen vsock://:1024
  echo "marooned-agent exited $?; restarting"
  sleep 1
done
INIT
chmod +x "${out}/rootfs/sbin/init"

echo "packing virtio+ext4 initramfs"
ird="${out}/initrd-root"
rm -rf "$ird"
mkdir -p "$ird"/{bin,dev,proc,sys,newroot,lib/modules}
# Copy the real binary; alpine's /bin/busybox is often a symlink.
cp -L "${out}/rootfs/bin/busybox" "$ird/bin/busybox"
chmod 0755 "$ird/bin/busybox"
ln -sf busybox "$ird/bin/sh"
# busybox is dynamically linked against musl. Without the loader, the kernel
# reports Failed to execute /init (error -2).
mkdir -p "$ird/lib"
cp -L "${out}/rootfs/lib/ld-musl-x86_64.so.1" "$ird/lib/"
ln -sf ld-musl-x86_64.so.1 "$ird/lib/libc.musl-x86_64.so.1"
modroot=$(find "${out}/kpkg" -type d -name '6.*-lts' | head -1)
copy_mod() {
  local n="$1"
  local f
  f=$(find "${modroot}" -name "${n}.ko.gz" | head -1)
  [ -n "$f" ] || return 0
  gzip -dc "$f" > "$ird/lib/modules/${n}.ko"
}
for m in virtio virtio_ring virtio_pci virtio_pci_legacy_dev virtio_pci_modern_dev virtio_blk \
         crc16 libcrc32c crc32c_generic crc32c-intel mbcache jbd2 ext4 \
         vsock vmw_vsock_virtio_transport_common vmw_vsock_virtio_transport; do
  copy_mod "$m"
done
# Kernel looks for /init. Shebang must resolve inside the initramfs (ENOENT
# on /bin/busybox was "Failed to execute /init (error -2)").
cat > "$ird/init" << 'IR'
#!/bin/sh
BB=/bin/busybox
exec > /dev/console 2>&1
echo "marooned-initrd: start"
$BB mount -t proc proc /proc
$BB mount -t sysfs sys /sys
$BB mount -t devtmpfs dev /dev || $BB mount -t tmpfs tmpfs /dev
mkdir -p /newroot
for m in virtio virtio_ring virtio_pci_legacy_dev virtio_pci_modern_dev virtio_pci virtio_blk \
         crc16 libcrc32c crc32c_generic crc32c-intel mbcache jbd2 ext4 \
         vsock vmw_vsock_virtio_transport_common vmw_vsock_virtio_transport; do
  [ -f /lib/modules/${m}.ko ] && $BB insmod /lib/modules/${m}.ko && echo "insmod $m"
done
i=0
while [ "$i" -lt 20 ]; do
  [ -b /dev/vda ] || [ -b /dev/vda1 ] && break
  $BB sleep 1
  i=$((i+1))
done
$BB ls -l /dev/vd* /dev/nvme* /dev/sda* 2>/dev/null || true
if ! $BB mount -t ext4 /dev/vda /newroot; then
  if ! $BB mount -t ext4 /dev/vda1 /newroot; then
    echo "marooned-initrd: mount root failed"
    exec $BB sh
  fi
fi
echo "marooned-initrd: switch_root"
exec $BB switch_root /newroot /sbin/init
IR
chmod 0755 "$ird/init"
( cd "$ird" && find . | cpio -o -H newc | gzip -9 > "${out}/kernel/initrd" )
echo "initrd contains:"
gzip -dc "${out}/kernel/initrd" | cpio -t | grep -E '^\./init$|busybox|^./bin/sh' || true

echo "creating ext4 qcow2"
rm -f "${out}/disk.raw" "${out}/disk.qcow2"
truncate -s 128M "${out}/disk.raw"
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
