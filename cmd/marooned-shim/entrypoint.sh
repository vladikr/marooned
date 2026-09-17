#!/bin/sh
set -e
if [ -x /app/marooned-oci ] && [ -d /host-opt ]; then
  cp /app/marooned-oci /host-opt/marooned-oci
  chmod 0755 /host-opt/marooned-oci
fi
exec /app/marooned_shim "$@"
