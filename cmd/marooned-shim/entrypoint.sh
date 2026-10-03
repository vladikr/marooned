#!/bin/sh
# Host copies are best-effort. Never fail before exec: CRI-O needs cri.sock
# even when /opt/marooned/marooned-oci already exists (busybox cp without
# -f exits EEXIST) or chmod on the hostPath is denied.
mkdir -p /var/run/marooned || true
chmod 1777 /var/run/marooned 2>/dev/null || echo "marooned-shim: chmod 1777 /var/run/marooned failed"
rm -f /var/run/marooned/cri.sock 2>/dev/null || true

install_oci() {
  dest="$1"
  dir=$(dirname "$dest")
  [ -d "$dir" ] || return 0
  [ -x /app/marooned-oci ] || return 0
  rm -f "$dest" 2>/dev/null || true
  cp -f /app/marooned-oci "$dest" 2>/dev/null || echo "marooned-shim: could not install $dest"
  chmod 0755 "$dest" 2>/dev/null || true
}

# Enter host pid 1's namespaces without hostPID on the Pod.
# /host-proc is hostPath /proc (see the shim DaemonSet).
host_nsenter() {
  if [ -e /host-proc/1/ns/mnt ] && [ -x /usr/bin/nsenter ]; then
    nsenter --mount=/host-proc/1/ns/mnt --uts=/host-proc/1/ns/uts \
      --ipc=/host-proc/1/ns/ipc --net=/host-proc/1/ns/net \
      --pid=/host-proc/1/ns/pid -- "$@"
    return $?
  fi
  return 1
}

handler_loaded() {
  host_nsenter crio config 2>/dev/null | grep -q 'crio.runtime.runtimes.marooned'
}

ensure_crio_handler() {
  [ -d /host-crio-dropin ] || return 0
  [ -f /app/20-marooned.conf ] || return 0
  mkdir -p /host-opt
  stamp_new="$(md5sum /app/marooned-oci /app/20-marooned.conf 2>/dev/null | md5sum | awk '{print $1}')"
  stamp_file="/host-opt/.crio-stamp"
  stamp_old="$(cat "$stamp_file" 2>/dev/null || true)"
  cp -f /app/20-marooned.conf /host-crio-dropin/20-marooned.conf 2>/dev/null || \
    echo "marooned-shim: could not write CRI-O drop-in"

  loaded=1
  handler_loaded || loaded=0
  if [ "$stamp_new" = "$stamp_old" ] && [ "$loaded" = 1 ]; then
    return 0
  fi

  echo "marooned-shim: ensuring CRI-O handler marooned (stamp=$stamp_new loaded=$loaded)"
  host_nsenter systemctl reload crio 2>/dev/null || true
  sleep 1
  if handler_loaded; then
    echo "$stamp_new" >"$stamp_file" 2>/dev/null || true
    echo "marooned-shim: CRI-O handler marooned loaded after reload"
    return 0
  fi
  echo "marooned-shim: reload left handler missing; restarting CRI-O"
  if host_nsenter systemctl restart crio; then
    sleep 2
    host_nsenter systemctl is-active crio >/dev/null 2>&1 || \
      echo "marooned-shim: crio not active after restart"
  else
    echo "marooned-shim: wrote drop-in; run ./hack/kubevirtci-install-crio-handler.sh to force-repair"
    return 0
  fi
  if handler_loaded; then
    echo "$stamp_new" >"$stamp_file" 2>/dev/null || true
    echo "marooned-shim: CRI-O handler marooned loaded after restart"
  else
    echo "marooned-shim: handler marooned still missing after restart"
  fi
}

install_oci /host-opt/marooned-oci
install_oci /host-usr-local-bin/marooned-oci
ensure_crio_handler
exec /app/marooned_shim "$@"
