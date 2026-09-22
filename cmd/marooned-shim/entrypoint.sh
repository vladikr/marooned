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

install_oci /host-opt/marooned-oci
install_oci /host-usr-local-bin/marooned-oci

if [ -d /host-crio-dropin ] && [ -f /app/20-marooned.conf ]; then
  rm -f /host-crio-dropin/.marooned-stamp
  mkdir -p /host-opt
  stamp_new="$(md5sum /app/marooned-oci /app/20-marooned.conf 2>/dev/null | md5sum | awk '{print $1}')"
  stamp_file="/host-opt/.crio-stamp"
  stamp_old="$(cat "$stamp_file" 2>/dev/null || true)"
  cp -f /app/20-marooned.conf /host-crio-dropin/20-marooned.conf 2>/dev/null || \
    echo "marooned-shim: could not write CRI-O drop-in"
  if [ "$stamp_new" != "$stamp_old" ]; then
    echo "$stamp_new" >"$stamp_file" 2>/dev/null || true
    if [ -x /usr/bin/nsenter ]; then
      nsenter -t 1 -m -u -i -n -p -- systemctl restart crio 2>/dev/null || \
        echo "marooned-shim: wrote CRI-O drop-in; restart crio on the host if handler is missing"
    else
      echo "marooned-shim: wrote CRI-O drop-in; restart crio on the host"
    fi
  fi
fi
exec /app/marooned_shim "$@"
