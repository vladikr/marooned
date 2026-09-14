#!/bin/sh
# Minimal PID 1 for the sandbox guest. Mounts essential filesystems and execs the agent.
set -e
mount -t proc proc /proc 2>/dev/null || true
mount -t sysfs sysfs /sys 2>/dev/null || true
mount -t devtmpfs devtmpfs /dev 2>/dev/null || true
mkdir -p /dev/pts /run /tmp
mount -t devpts devpts /dev/pts 2>/dev/null || true
mount -t tmpfs tmpfs /run 2>/dev/null || true
echo "marooned-agent starting"
exec /usr/local/bin/marooned-agent -listen tcp://0.0.0.0:1024
