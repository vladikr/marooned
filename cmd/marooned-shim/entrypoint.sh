#!/bin/sh
set -e
# World-writable so uid 107 can bind sockets once the shim creates <uid>/.
mkdir -p /var/run/marooned || true
chmod 1777 /var/run/marooned || echo "marooned-shim: chmod 1777 /var/run/marooned failed"
if [ -x /app/marooned-oci ]; then
  if [ -d /host-opt ]; then
    cp /app/marooned-oci /host-opt/marooned-oci
    chmod 0755 /host-opt/marooned-oci
  fi
  if [ -d /host-usr-local-bin ]; then
    cp /app/marooned-oci /host-usr-local-bin/marooned-oci
    chmod 0755 /host-usr-local-bin/marooned-oci
  fi
fi
if [ -d /host-crio-dropin ] && [ -f /app/20-marooned.conf ]; then
  # CRI-O parses every file in conf.d as TOML. Stamp lives under /opt/marooned.
  rm -f /host-crio-dropin/.marooned-stamp
  mkdir -p /host-opt
  stamp_new="$(md5sum /app/marooned-oci /app/20-marooned.conf 2>/dev/null | md5sum | awk '{print $1}')"
  stamp_file="/host-opt/.crio-stamp"
  stamp_old="$(cat "$stamp_file" 2>/dev/null || true)"
  cp /app/20-marooned.conf /host-crio-dropin/20-marooned.conf
  if [ "$stamp_new" != "$stamp_old" ]; then
    echo "$stamp_new" >"$stamp_file"
    if [ -x /usr/bin/nsenter ]; then
      nsenter -t 1 -m -u -i -n -p -- systemctl restart crio 2>/dev/null || \
        echo "marooned-shim: wrote CRI-O drop-in; restart crio on the host if handler is missing"
    else
      echo "marooned-shim: wrote CRI-O drop-in; restart crio on the host"
    fi
  fi
fi
exec /app/marooned_shim "$@"
