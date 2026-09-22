#!/usr/bin/env bash
# Install the marooned CRI-O runtime handler on kubevirtci nodes.
# Run from the marooned repo after cluster-up and after the shim DaemonSet is running
# (it copies marooned-oci to /opt/marooned on the node).
#
#   export KUBEVIRT_PROVIDER=k8s-1.37
#   ./hack/kubevirtci-install-crio-handler.sh
set -eo pipefail
root="$(cd "$(dirname "$0")/.." && pwd -P)"
cd "$root"
# shellcheck source=hack/kubevirt-cluster.sh
source ./hack/kubevirt-cluster.sh
ssh="./cluster-up/ssh.sh"
conf="${root}/hack/crio/20-marooned.conf"

nodes="${KUBEVIRT_NUM_NODES:-1}"
for i in $(seq 1 "$nodes"); do
  node=$(printf 'node%02d' "$i")
  echo "installing marooned CRI-O handler on $node"
  "$ssh" "$node" "sudo mkdir -p /usr/local/bin /run/marooned-oci /var/run/marooned /etc/crio/crio.conf.d /opt/marooned; \
    sudo chmod 1777 /var/run/marooned /run/marooned-oci /opt/marooned; \
    sudo chcon -Rt container_file_t /var/run/marooned /run/marooned-oci /opt/marooned 2>/dev/null || true; \
    if [ -x /opt/marooned/marooned-oci ]; then \
      sudo cp /opt/marooned/marooned-oci /usr/local/bin/marooned-oci; \
    fi; \
    sudo chmod 0755 /usr/local/bin/marooned-oci; \
    file /usr/local/bin/marooned-oci; \
    ls -l /usr/local/bin/marooned-oci"
  # drop-in (small): pipe via ssh
  #"$ssh" "$node" "sudo tee /etc/crio/crio.conf.d/20-marooned.conf >/dev/null" < "$conf"
  # Encode as a single string to survive the ssh.sh wrapper's newline flattening
  conf_b64=$(base64 -w0 "$conf")
  "$ssh" "$node" "echo ${conf_b64} | base64 -d | sudo tee /etc/crio/crio.conf.d/20-marooned.conf >/dev/null; sudo rm -f /etc/crio/crio.conf.d/.marooned-stamp"
  "$ssh" "$node" "sudo systemctl restart crio && sleep 2 && sudo systemctl is-active crio"
  # Example pod uses this image with imagePullPolicy Never (docker.io rate-limits).
  "$ssh" "$node" "sudo crictl pull quay.io/prometheus/busybox:latest >/dev/null || true"
  "$ssh" "$node" "sudo crictl pull public.ecr.aws/docker/library/python:3.12-alpine >/dev/null || true"
  "$ssh" "$node" "sudo crictl pull public.ecr.aws/docker/library/nginx:alpine >/dev/null || true"
  # VMI containerDisks are IfNotPresent; refresh :latest after cluster-push.
  "$ssh" "$node" "sudo crictl pull registry:5000/marooned-sandbox:latest >/dev/null || true"
  "$ssh" "$node" "sudo crictl pull registry:5000/marooned-kernel:latest >/dev/null || true"
  "$ssh" "$node" "sudo crictl pull registry:5000/marooned-vsockfwd:latest >/dev/null || true"
done
echo "done. RuntimeClass handler 'marooned' should resolve on the nodes."
echo "Then: kubectl apply -f examples/sandbox-pod.yaml"
