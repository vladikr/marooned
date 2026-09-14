#!/bin/sh
# Placeholder agent used until the compiled marooned-agent binary is copied into
# the image. The real binary is cmd/marooned-agent.
echo "marooned-agent placeholder; replace with the compiled binary" >&2
# Keep the guest alive so the VMI stays Running during bring-up.
exec sleep infinity
